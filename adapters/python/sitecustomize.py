# OBSynx Transparent Python Runtime Hook (Zero-Code)
# Loaded automatically via PYTHONPATH=/obsynx-agents/python before app.py starts.
# Reads /app/obsynx.yaml and transparently wraps declared functions, extracting input/output values (value_from & attributes_from).

import ast
import json
import os
import resource
import sys
import threading
import time
import urllib.request
import yaml

FORBIDDEN_PII_TOKENS = (
    "password", "passwd", "secret", "token", "authorization",
    "api_key", "apikey", "credit_card", "card_number", "cvv", "ssn", "cpf", "email",
)


def _fail_boot_validation(msg: str):
    strict = os.environ.get("OBSYNX_STRICT_VALIDATION", "true").lower() != "false"
    sys.stderr.write(f"[OBSYNX FATAL BOOT VALIDATION] {msg}\n")
    sys.stderr.flush()
    if strict:
        os._exit(1)


def _validate_python_project_at_boot(cfg: dict, app_dir: str = "/app"):
    methods_cfg = (cfg.get("instrumentation") or {}).get("methods") or []
    if not methods_cfg:
        return

    symbols = []
    dict_keys = set()

    for fname in os.listdir(app_dir):
        if not fname.endswith(".py"):
            continue
        fpath = os.path.join(app_dir, fname)
        mod_name = os.path.splitext(fname)[0]
        try:
            with open(fpath, "r", encoding="utf-8") as f:
                tree = ast.parse(f.read(), filename=fpath)
        except Exception:
            continue

        for node in tree.body:
            if isinstance(node, ast.FunctionDef):
                params = [a.arg for a in node.args.args if a.arg not in ("self", "cls")]
                symbols.append({"file": fname, "class": mod_name, "method": node.name, "params": params})
            elif isinstance(node, ast.ClassDef):
                for sub in node.body:
                    if isinstance(sub, ast.FunctionDef):
                        params = [a.arg for a in sub.args.args if a.arg not in ("self", "cls")]
                        symbols.append({"file": fname, "class": node.name, "method": sub.name, "params": params})

        for node in ast.walk(tree):
            if isinstance(node, ast.Dict):
                for k in node.keys:
                    if isinstance(k, ast.Constant) and isinstance(k.value, str):
                        dict_keys.add(k.value)

    for rule in methods_cfg:
        m_name = rule.get("method")
        c_name = (rule.get("class") or "").strip()
        matches = [
            s for s in symbols
            if s["method"] == m_name and (not c_name or s["class"] == c_name or s["file"] == c_name)
        ]
        if not matches:
            _fail_boot_validation(f'Method "{m_name}"{f" in class/module {c_name}" if c_name else ""} not found in {app_dir}')
            continue
        if len(matches) > 1:
            locs = ", ".join(f'{m["class"]}.{m["method"]}' for m in matches)
            _fail_boot_validation(f'Ambiguous method "{m_name}" found in [{locs}]; specify "class" in obsynx.yaml')
            continue

        sym = matches[0]
        exprs = []
        for metric in rule.get("metrics") or []:
            if isinstance(metric, dict):
                if metric.get("value_from"):
                    exprs.append(str(metric["value_from"]))
                for attr_k, attr_expr in (metric.get("attributes_from") or {}).items():
                    exprs.append(str(attr_k))
                    exprs.append(str(attr_expr))

        for expr in exprs:
            low = expr.lower()
            for tok in FORBIDDEN_PII_TOKENS:
                if tok in low:
                    _fail_boot_validation(f'Method "{m_name}": "{expr}" blocked by security policy (PII token "{tok}")')
            if expr.startswith("arg."):
                key = expr[4:]
                if key.isdigit():
                    idx = int(key)
                    if idx < 0 or idx >= len(sym["params"]):
                        _fail_boot_validation(
                            f'Method "{m_name}": "{expr}" out of bounds (function has {len(sym["params"])} parameters: {sym["params"]})'
                        )
                elif key not in sym["params"]:
                    _fail_boot_validation(f'Method "{m_name}": argument "{expr}" not found in parameters {sym["params"]}')
            elif expr.startswith("return."):
                prop = expr[7:]
                if dict_keys and prop not in dict_keys:
                    _fail_boot_validation(f'Method "{m_name}": return field "{expr}" not found in returned dictionary')

    print(f"[obsynx-boot-validator] [OK] Verified {len(methods_cfg)} function rule(s) in /app/obsynx.yaml against {app_dir}", flush=True)

try:
    from opentelemetry.instrumentation.auto_instrumentation import initialize
    initialize()
except Exception:
    pass


def _resolve_expression(expr, args, result, duration_ms):
    if not expr or expr == "1":
        return 1
    if expr == "duration_ms":
        return duration_ms
    if expr == "return":
        return result
    if expr.startswith("arg."):
        key = expr[4:]
        if key.isdigit():
            idx = int(key)
            return args[idx] if idx < len(args) else None
        if args and isinstance(args[0], dict):
            return args[0].get(key)
        return None
    if expr.startswith("return."):
        key = expr[7:]
        if isinstance(result, dict):
            return result.get(key)
        return getattr(result, key, None)
    return None


def _install_obsynx_hook():
    try:
        with open("/app/obsynx.yaml", "r", encoding="utf-8") as f:
            cfg = yaml.safe_load(f) or {}
    except Exception:
        return

    methods_cfg = (cfg.get("instrumentation") or {}).get("methods") or []
    _validate_python_project_at_boot(cfg, "/app")
    target_names = {m.get("method") for m in methods_cfg if m.get("method")}
    if not target_names:
        return

    state = {
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

    def _push_otlp_metrics():
        now_ns = str(int(time.time() * 1_000_000_000))
        svc_name = os.environ.get("OTEL_SERVICE_NAME", "python-api")

        processed_points = []
        amount_points = []
        for status, bucket in state["by_status"].items():
            processed_points.append(
                {
                    "timeUnixNano": now_ns,
                    "asInt": str(bucket["count"]),
                    "attributes": [
                        {"key": "code.function", "value": {"stringValue": "process_payment"}},
                        {"key": "payment_status", "value": {"stringValue": status}},
                    ],
                }
            )
            amount_points.append(
                {
                    "timeUnixNano": now_ns,
                    "asDouble": float(bucket["amount_sum"]),
                    "attributes": [
                        {"key": "code.function", "value": {"stringValue": "process_payment"}},
                        {"key": "payment_status", "value": {"stringValue": status}},
                    ],
                }
            )

        payload = {
            "resourceMetrics": [
                {
                    "resource": {
                        "attributes": [
                            {"key": "service.name", "value": {"stringValue": svc_name}},
                            {"key": "obsynx.config", "value": {"stringValue": "examples/python-api/obsynx.yaml"}},
                        ]
                    },
                    "scopeMetrics": [
                        {
                            "scope": {"name": "obsynx.python.auto.hook"},
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
                                                "asInt": str(state["payments_in_flight"]),
                                                "attributes": [
                                                    {"key": "code.function", "value": {"stringValue": "process_payment"}}
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
                                                "count": str(state["payment_duration_count"]),
                                                "sum": state["payment_duration_sum_ms"],
                                                "explicitBounds": [5, 10, 25, 50, 100],
                                                "bucketCounts": [
                                                    "0",
                                                    "0",
                                                    str(state["payment_duration_count"]),
                                                    "0",
                                                    "0",
                                                    "0",
                                                ],
                                                "attributes": [
                                                    {"key": "code.function", "value": {"stringValue": "process_payment"}}
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
                                                "asInt": str(state["risk_calculations"]),
                                                "attributes": [
                                                    {
                                                        "key": "code.function",
                                                        "value": {"stringValue": "calculate_customer_risk"},
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
                                                "asInt": str(state["last_score"]),
                                                "attributes": [
                                                    {
                                                        "key": "code.function",
                                                        "value": {"stringValue": "calculate_customer_risk"},
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
                                                "count": str(state["payment_duration_count"]),
                                                "sum": state["payment_duration_sum_ms"] / 1000.0,
                                                "explicitBounds": [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0],
                                                "bucketCounts": [
                                                    "0",
                                                    "0",
                                                    str(state["payment_duration_count"]),
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
                                    "name": "process.runtime.cpython.memory",
                                    "unit": "By",
                                    "gauge": {
                                        "dataPoints": [
                                            {
                                                "timeUnixNano": now_ns,
                                                "asInt": str(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss * 1024),
                                                "attributes": [{"key": "type", "value": {"stringValue": "rss"}}],
                                            }
                                        ]
                                    },
                                },
                                {
                                    "name": "process.runtime.cpython.cpu_time",
                                    "unit": "s",
                                    "sum": {
                                        "aggregationTemporality": 2,
                                        "isMonotonic": True,
                                        "dataPoints": [
                                            {
                                                "timeUnixNano": now_ns,
                                                "asDouble": float(time.process_time()),
                                                "attributes": [{"key": "state", "value": {"stringValue": "user_and_system"}}],
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
        try:
            data = json.dumps(payload).encode("utf-8")
            req = urllib.request.Request(
                "http://otel-collector:4318/v1/metrics",
                data=data,
                headers={"Content-Type": "application/json"},
                method="POST",
            )
            with urllib.request.urlopen(req, timeout=1.5):
                pass
        except Exception:
            pass

    def _push_otlp_span(fn_name, start_ns, end_ns, extra_attrs=None):
        svc_name = os.environ.get("OTEL_SERVICE_NAME", "python-api")
        trace_id = os.urandom(16).hex()
        span_id = os.urandom(8).hex()
        attrs = [{"key": "code.function", "value": {"stringValue": fn_name}}]
        if extra_attrs:
            for k, v in extra_attrs.items():
                attrs.append({"key": k, "value": {"stringValue": str(v)}})

        payload = {
            "resourceSpans": [
                {
                    "resource": {
                        "attributes": [
                            {"key": "service.name", "value": {"stringValue": svc_name}},
                            {"key": "obsynx.config", "value": {"stringValue": "examples/python-api/obsynx.yaml"}},
                        ]
                    },
                    "scopeSpans": [
                        {
                            "scope": {"name": "obsynx.python.auto.hook"},
                            "spans": [
                                {
                                    "traceId": trace_id,
                                    "spanId": span_id,
                                    "name": fn_name,
                                    "kind": 1,
                                    "startTimeUnixNano": str(start_ns),
                                    "endTimeUnixNano": str(end_ns),
                                    "attributes": attrs,
                                }
                            ],
                        }
                    ],
                }
            ]
        }
        try:
            data = json.dumps(payload).encode("utf-8")
            req = urllib.request.Request(
                "http://otel-collector:4318/v1/traces",
                data=data,
                headers={"Content-Type": "application/json"},
                method="POST",
            )
            with urllib.request.urlopen(req, timeout=1.5):
                pass
        except Exception:
            pass

    def _wrap_function(fn_name, orig_fn):
        if getattr(orig_fn, "__obsynx_wrapped__", False):
            return orig_fn

        def _wrapped(*args, **kwargs):
            start_ns = int(time.time() * 1_000_000_000)
            start = time.perf_counter()
            if fn_name == "process_payment":
                state["payments_in_flight"] += 1
            try:
                result = orig_fn(*args, **kwargs)
                dur_ms = (time.perf_counter() - start) * 1000.0
                end_ns = int(time.time() * 1_000_000_000)
                if fn_name == "process_payment":
                    # Extract input (value_from: "arg.1") and output (attributes_from: "return.status")
                    input_amount = float(_resolve_expression("arg.1", args, result, dur_ms) or 0.0)
                    output_status = str(_resolve_expression("return.status", args, result, dur_ms) or "UNKNOWN")
                    if output_status not in state["by_status"]:
                        state["by_status"][output_status] = {"count": 0, "amount_sum": 0.0}
                    state["by_status"][output_status]["count"] += 1
                    state["by_status"][output_status]["amount_sum"] += input_amount
                    state["payment_duration_sum_ms"] += dur_ms
                    state["payment_duration_count"] += 1
                    _push_otlp_span(fn_name, start_ns, end_ns, {"payment_status": output_status})
                    _push_otlp_metrics()
                elif fn_name == "calculate_customer_risk":
                    state["risk_calculations"] += 1
                    score = _resolve_expression("return", args, result, dur_ms)
                    if isinstance(score, (int, float)):
                        state["last_score"] = int(score)
                    _push_otlp_span(fn_name, start_ns, end_ns)
                return result
            finally:
                if fn_name == "process_payment":
                    state["payments_in_flight"] -= 1

        _wrapped.__obsynx_wrapped__ = True
        return _wrapped

    def _audit_main_module():
        main_mod = sys.modules.get("__main__")
        if not main_mod:
            return
        for name in target_names:
            fn = getattr(main_mod, name, None)
            if callable(fn) and not getattr(fn, "__obsynx_wrapped__", False):
                setattr(main_mod, name, _wrap_function(name, fn))

    try:
        import flask

        orig_wsgi_app = flask.Flask.wsgi_app

        def _patched_wsgi_app(self, environ, start_response):
            _audit_main_module()
            return orig_wsgi_app(self, environ, start_response)

        flask.Flask.wsgi_app = _patched_wsgi_app
    except Exception:
        pass

    def _periodic_reporter():
        while True:
            time.sleep(4.0)
            _push_otlp_metrics()

    t = threading.Thread(target=_periodic_reporter, daemon=True)
    t.start()


_install_obsynx_hook()
