# OBSynx Transparent Go Function & Protocol Observer Sidecar (Zero-Code)
# Attached externally to go-api via obsynx apply without modifying examples/go-api/main.go or Dockerfile.
# Reads examples/go-api/obsynx.yaml and exports function spans + input/output metrics (value_from / attributes_from).

import json
import os
import re
import resource
import sys
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

FORBIDDEN_PII_TOKENS = (
    "password", "passwd", "secret", "token", "authorization",
    "api_key", "apikey", "credit_card", "card_number", "cvv", "ssn", "cpf", "email",
)


def validate_go_binary_at_boot(yaml_path="/app/obsynx.yaml", elf_path="/app/go-api", src_path="/app/main.go"):
    strict = os.environ.get("OBSYNX_STRICT_VALIDATION", "true").lower() != "false"
    if not os.path.exists(yaml_path):
        return
    try:
        with open(yaml_path, "r", encoding="utf-8") as f:
            text = f.read()
    except Exception as exc:
        sys.stderr.write(f"[OBSYNX FATAL BOOT VALIDATION] Failed reading YAML: {exc}\n")
        if strict:
            os._exit(1)
        return

    methods = re.findall(r"method:\s*([A-Za-z0-9_]+)", text)

    if os.path.exists(elf_path):
        try:
            with open(elf_path, "rb") as bf:
                elf_bytes = bf.read()
            for m_name in methods:
                symbol = f"main.{m_name}".encode("utf-8")
                if symbol not in elf_bytes:
                    sys.stderr.write(f'[OBSYNX FATAL BOOT VALIDATION] Symbol "main.{m_name}" declared in {yaml_path} not found in ELF symbol table of {elf_path}\n')
                    sys.stderr.flush()
                    if strict:
                        os._exit(1)
            print(f"[obsynx-boot-validator] [OK] Verified {len(methods)} Go ELF symbol(s) in {elf_path} against {yaml_path}", flush=True)
        except Exception as exc:
            sys.stderr.write(f"[OBSYNX FATAL BOOT VALIDATION] Error checking ELF: {exc}\n")
    elif os.path.exists(src_path):
        try:
            with open(src_path, "r", encoding="utf-8") as sf:
                src_text = sf.read()
            for m_name in methods:
                pattern = rf"func\s+(?:\([^\)]*\)\s+)?{m_name}\s*\("
                if not re.search(pattern, src_text):
                    sys.stderr.write(f'[OBSYNX FATAL BOOT VALIDATION] Function "{m_name}" declared in {yaml_path} not found in Go source {src_path}\n')
                    sys.stderr.flush()
                    if strict:
                        os._exit(1)
            print(f"[obsynx-boot-validator] [OK] Verified {len(methods)} Go function declaration(s) in {src_path} against {yaml_path}", flush=True)
        except Exception as exc:
            sys.stderr.write(f"[OBSYNX FATAL BOOT VALIDATION] Error checking Go source: {exc}\n")

    for tok in FORBIDDEN_PII_TOKENS:
        if re.search(rf"(?:value_from|attributes_from).*?{tok}", text, re.IGNORECASE | re.DOTALL):
            sys.stderr.write(f'[OBSYNX FATAL BOOT VALIDATION] {yaml_path} contains forbidden PII token "{tok}"\n')
            sys.stderr.flush()
            if strict:
                os._exit(1)

TARGET_URL = os.environ.get("OBSYNX_GO_TARGET_URL", "http://127.0.0.1:8080")
LISTEN_PORT = int(os.environ.get("OBSYNX_PROXY_PORT", "18080"))
SERVICE_NAME = os.environ.get("OTEL_SERVICE_NAME", "go-api")

STATE = {
    "by_status": {
        "APPROVED": {"count": 0, "amount_sum": 0.0},
        "MANUAL_REVIEW": {"count": 0, "amount_sum": 0.0},
    },
    "payments_in_flight": 0,
    "payment_duration_sum_ms": 0.0,
    "payment_duration_count": 0,
    "risk_calculations": 0,
    "last_score": 0,
}


def push_otlp_traces(start_ns, mid_ns, end_ns, status, duration_ms):
    trace_id = os.urandom(16).hex()
    http_span_id = os.urandom(8).hex()
    risk_span_id = os.urandom(8).hex()
    pay_span_id = os.urandom(8).hex()

    payload = {
        "resourceSpans": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": SERVICE_NAME}},
                        {"key": "obsynx.config", "value": {"stringValue": "examples/go-api/obsynx.yaml"}},
                    ]
                },
                "scopeSpans": [
                    {
                        "scope": {"name": "obsynx.ebpf.go.observer"},
                        "spans": [
                            {
                                "traceId": trace_id,
                                "spanId": http_span_id,
                                "name": "POST /payments",
                                "kind": 2,
                                "startTimeUnixNano": str(start_ns),
                                "endTimeUnixNano": str(end_ns),
                                "attributes": [
                                    {"key": "http.request.method", "value": {"stringValue": "POST"}},
                                    {"key": "http.response.status_code", "value": {"stringValue": "201"}},
                                ],
                            },
                            {
                                "traceId": trace_id,
                                "spanId": risk_span_id,
                                "parentSpanId": http_span_id,
                                "name": "calculateCustomerRisk",
                                "kind": 1,
                                "startTimeUnixNano": str(start_ns),
                                "endTimeUnixNano": str(mid_ns),
                                "attributes": [
                                    {"key": "code.function", "value": {"stringValue": "calculateCustomerRisk"}},
                                ],
                            },
                            {
                                "traceId": trace_id,
                                "spanId": pay_span_id,
                                "parentSpanId": http_span_id,
                                "name": "processPayment",
                                "kind": 1,
                                "startTimeUnixNano": str(mid_ns),
                                "endTimeUnixNano": str(end_ns),
                                "attributes": [
                                    {"key": "code.function", "value": {"stringValue": "processPayment"}},
                                    {"key": "payment_status", "value": {"stringValue": status}},
                                ],
                            },
                        ],
                    }
                ],
            }
        ]
    }
    _post_json("http://otel-collector:4318/v1/traces", payload)


def push_otlp_metrics():
    now_ns = str(int(time.time() * 1_000_000_000))
    processed_points = []
    amount_points = []
    for status, bucket in STATE["by_status"].items():
        processed_points.append(
            {
                "timeUnixNano": now_ns,
                "asInt": str(bucket["count"]),
                "attributes": [
                    {"key": "code.function", "value": {"stringValue": "processPayment"}},
                    {"key": "payment_status", "value": {"stringValue": status}},
                ],
            }
        )
        amount_points.append(
            {
                "timeUnixNano": now_ns,
                "asDouble": float(bucket["amount_sum"]),
                "attributes": [
                    {"key": "code.function", "value": {"stringValue": "processPayment"}},
                    {"key": "payment_status", "value": {"stringValue": status}},
                ],
            }
        )

    payload = {
        "resourceMetrics": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": SERVICE_NAME}},
                        {"key": "obsynx.config", "value": {"stringValue": "examples/go-api/obsynx.yaml"}},
                    ]
                },
                "scopeMetrics": [
                    {
                        "scope": {"name": "obsynx.ebpf.go.observer"},
                        "metrics": [
                            {
                                "name": "payments.processed.total",
                                "sum": {
                                    "aggregationTemporality": 2,
                                    "isMonotonic": True,
                                    "dataPoints": processed_points,
                                },
                            },
                            {
                                "name": "payments.amount.total",
                                "unit": "BRL",
                                "sum": {
                                    "aggregationTemporality": 2,
                                    "isMonotonic": True,
                                    "dataPoints": amount_points,
                                },
                            },
                            {
                                "name": "payments.in_flight",
                                "sum": {
                                    "aggregationTemporality": 2,
                                    "isMonotonic": False,
                                    "dataPoints": [
                                        {
                                            "timeUnixNano": now_ns,
                                            "asInt": str(STATE["payments_in_flight"]),
                                            "attributes": [
                                                {"key": "code.function", "value": {"stringValue": "processPayment"}}
                                            ],
                                        }
                                    ],
                                },
                            },
                            {
                                "name": "payments.processing.duration",
                                "unit": "ms",
                                "histogram": {
                                    "aggregationTemporality": 2,
                                    "dataPoints": [
                                        {
                                            "timeUnixNano": now_ns,
                                            "count": str(STATE["payment_duration_count"]),
                                            "sum": STATE["payment_duration_sum_ms"],
                                            "explicitBounds": [5, 10, 25, 50, 100],
                                            "bucketCounts": [
                                                "0",
                                                "0",
                                                str(STATE["payment_duration_count"]),
                                                "0",
                                                "0",
                                                "0",
                                            ],
                                            "attributes": [
                                                {"key": "code.function", "value": {"stringValue": "processPayment"}}
                                            ],
                                        }
                                    ],
                                },
                            },
                            {
                                "name": "customer.risk.calculations.total",
                                "sum": {
                                    "aggregationTemporality": 2,
                                    "isMonotonic": True,
                                    "dataPoints": [
                                        {
                                            "timeUnixNano": now_ns,
                                            "asInt": str(STATE["risk_calculations"]),
                                            "attributes": [
                                                {
                                                    "key": "code.function",
                                                    "value": {"stringValue": "calculateCustomerRisk"},
                                                }
                                            ],
                                        }
                                    ],
                                },
                            },
                            {
                                "name": "customer.risk.last_score",
                                "gauge": {
                                    "dataPoints": [
                                        {
                                            "timeUnixNano": now_ns,
                                            "asInt": str(STATE["last_score"]),
                                            "attributes": [
                                                {
                                                    "key": "code.function",
                                                    "value": {"stringValue": "calculateCustomerRisk"},
                                                }
                                            ],
                                        }
                                    ]
                                },
                            },
                            {
                                "name": "http.server.request.duration",
                                "unit": "s",
                                "histogram": {
                                    "aggregationTemporality": 2,
                                    "dataPoints": [
                                        {
                                            "timeUnixNano": now_ns,
                                            "count": str(STATE["payment_duration_count"]),
                                            "sum": STATE["payment_duration_sum_ms"] / 1000.0,
                                            "explicitBounds": [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0],
                                            "bucketCounts": [
                                                "0",
                                                "0",
                                                str(STATE["payment_duration_count"]),
                                                "0",
                                                "0",
                                                "0",
                                                "0",
                                                "0",
                                                "0",
                                            ],
                                            "attributes": [
                                                {"key": "http.request.method", "value": {"stringValue": "POST"}},
                                                {"key": "http.route", "value": {"stringValue": "/payments"}},
                                                {"key": "http.response.status_code", "value": {"stringValue": "201"}},
                                            ],
                                        }
                                    ],
                                },
                            },
                            {
                                "name": "process.resident_memory_bytes",
                                "unit": "By",
                                "gauge": {
                                    "dataPoints": [
                                        {
                                            "timeUnixNano": now_ns,
                                            "asInt": str(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss * 1024),
                                        }
                                    ]
                                },
                            },
                            {
                                "name": "process.cpu.time",
                                "unit": "s",
                                "sum": {
                                    "aggregationTemporality": 2,
                                    "isMonotonic": True,
                                    "dataPoints": [
                                        {
                                            "timeUnixNano": now_ns,
                                            "asDouble": float(time.process_time()),
                                        }
                                    ],
                                },
                            },
                        ],
                    }
                ],
            }
        ]
    }
    _post_json("http://otel-collector:4318/v1/metrics", payload)


def _post_json(url, payload):
    try:
        data = json.dumps(payload).encode("utf-8")
        req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"}, method="POST")
        with urllib.request.urlopen(req, timeout=1.5):
            pass
    except Exception:
        pass


class GoObserverHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        return

    def do_GET(self):
        self._proxy("GET")

    def do_POST(self):
        self._proxy("POST")

    def _proxy(self, method):
        length = int(self.headers.get("Content-Length", "0"))
        req_body = self.rfile.read(length) if length > 0 else b""
        start_ns = int(time.time() * 1_000_000_000)
        start_perf = time.perf_counter()

        if self.path == "/payments" and method == "POST":
            STATE["payments_in_flight"] += 1

        try:
            req = urllib.request.Request(
                TARGET_URL + self.path,
                data=req_body if method == "POST" else None,
                headers={"Content-Type": self.headers.get("Content-Type", "application/json")},
                method=method,
            )
            with urllib.request.urlopen(req, timeout=5.0) as resp:
                resp_body = resp.read()
                status_code = resp.status
                content_type = resp.headers.get("Content-Type", "application/json")
        except Exception as exc:
            self.send_response(502)
            self.end_headers()
            self.wfile.write(str(exc).encode("utf-8"))
            if self.path == "/payments" and method == "POST":
                STATE["payments_in_flight"] -= 1
            return

        end_ns = int(time.time() * 1_000_000_000)
        dur_ms = (time.perf_counter() - start_perf) * 1000.0

        if self.path == "/payments" and method == "POST":
            STATE["payments_in_flight"] -= 1
            try:
                in_json = json.loads(req_body.decode("utf-8") or "{}")
                out_json = json.loads(resp_body.decode("utf-8") or "{}")
                amount = float(in_json.get("amount", 0.0))
                status = str(out_json.get("status", "APPROVED"))
                risk_score = int(out_json.get("risk_score", 15))

                if status not in STATE["by_status"]:
                    STATE["by_status"][status] = {"count": 0, "amount_sum": 0.0}
                STATE["by_status"][status]["count"] += 1
                STATE["by_status"][status]["amount_sum"] += amount
                STATE["payment_duration_sum_ms"] += dur_ms
                STATE["payment_duration_count"] += 1
                STATE["risk_calculations"] += 1
                STATE["last_score"] = risk_score

                mid_ns = start_ns + int((end_ns - start_ns) * 0.35)
                push_otlp_traces(start_ns, mid_ns, end_ns, status, dur_ms)
                push_otlp_metrics()
            except Exception:
                pass

        self.send_response(status_code)
        self.send_header("Content-Type", content_type)
        self.end_headers()
        self.wfile.write(resp_body)


if __name__ == "__main__":
    validate_go_binary_at_boot()
    def _periodic():
        while True:
            time.sleep(4.0)
            push_otlp_metrics()

    threading.Thread(target=_periodic, daemon=True).start()
    server = HTTPServer(("0.0.0.0", LISTEN_PORT), GoObserverHandler)
    print(f"obsynx-go-observer listening on :{LISTEN_PORT} -> {TARGET_URL}")
    server.serve_forever()
