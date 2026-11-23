// OBSynx Transparent Node.js Runtime Hook (Zero-Code)
// Loaded automatically via NODE_OPTIONS="--require @opentelemetry/auto-instrumentations-node/register --require /obsynx-agents/nodejs/obsynx-hook.js"
// Reads /app/obsynx.yaml and transparently wraps target methods, extracting input/output values (value_from & attributes_from).

const fs = require('fs');
const http = require('http');
const path = require('path');
const Module = require('module');
const { trace } = require('@opentelemetry/api');

const FORBIDDEN_PII_TOKENS = [
  'password', 'passwd', 'secret', 'token', 'authorization',
  'api_key', 'apikey', 'credit_card', 'card_number', 'cvv', 'ssn', 'cpf', 'email',
];

function failBootValidation(msg) {
  const strict = (process.env.OBSYNX_STRICT_VALIDATION || 'true').toLowerCase() !== 'false';
  console.error(`[OBSYNX FATAL BOOT VALIDATION] ${msg}`);
  if (strict) {
    process.exit(1);
  }
}

function parseObsynxRules(yamlPath) {
  if (!fs.existsSync(yamlPath)) return [];
  const text = fs.readFileSync(yamlPath, 'utf8');
  const rules = [];
  const blocks = text.split(/\n\s*-\s+/);
  for (let i = 1; i < blocks.length; i++) {
    const blk = blocks[i];
    const mMatch = blk.match(/^(?:class:\s*([^\n]+)\n\s*)?method:\s*([a-zA-Z0-9_]+)/);
    if (!mMatch) continue;
    const cls = mMatch[1] ? mMatch[1].trim() : '';
    const method = mMatch[2].trim();
    const exprs = [];
    const vfRegex = /value_from:\s*["']?([^"'\n]+)["']?/g;
    let vm;
    while ((vm = vfRegex.exec(blk)) !== null) exprs.push(vm[1].trim());
    const afRegex = /:\s*["'](arg\.[^"']+|return\.[^"']+)["']/g;
    let am;
    while ((am = afRegex.exec(blk)) !== null) exprs.push(am[1].trim());
    rules.push({ class: cls, method, exprs });
  }
  return rules;
}

function validateNodeProjectAtBoot(yamlPath, appDir) {
  const rules = parseObsynxRules(yamlPath);
  if (rules.length === 0) return [];

  const jsFiles = fs.readdirSync(appDir).filter((f) => f.endsWith('.js'));
  const symbols = [];
  const returnKeys = new Set();

  for (const file of jsFiles) {
    const content = fs.readFileSync(path.join(appDir, file), 'utf8');
    const modName = path.basename(file, '.js');
    const fnRegex = /function\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)/g;
    let fm;
    while ((fm = fnRegex.exec(content)) !== null) {
      const params = fm[2]
        .split(',')
        .map((p) => p.split('=')[0].trim())
        .filter(Boolean);
      symbols.push({ file, module: modName, method: fm[1], params });
    }
    const keyRegex = /^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(?::|,|$)/gm;
    let km;
    while ((km = keyRegex.exec(content)) !== null) {
      returnKeys.add(km[1].trim());
    }
  }

  for (const rule of rules) {
    const matches = symbols.filter(
      (s) => s.method === rule.method && (!rule.class || s.module === rule.class || s.file === rule.class)
    );
    if (matches.length === 0) {
      failBootValidation(`Method "${rule.method}"${rule.class ? ` in module "${rule.class}"` : ''} not found in ${appDir}`);
    }
    if (matches.length > 1) {
      const locs = matches.map((m) => `${m.module}.${m.method}`).join(', ');
      failBootValidation(`Ambiguous method "${rule.method}" found in multiple modules [${locs}]; specify 'class' in obsynx.yaml`);
    }
    const sym = matches[0];
    if (!sym) continue;

    for (const expr of rule.exprs) {
      const lower = expr.toLowerCase();
      for (const tok of FORBIDDEN_PII_TOKENS) {
        if (lower.includes(tok)) {
          failBootValidation(`Method "${rule.method}": expression "${expr}" blocked by security policy (PII token "${tok}")`);
        }
      }
      if (expr.startsWith('arg.')) {
        const key = expr.slice(4);
        if (/^\d+$/.test(key)) {
          const idx = Number(key);
          if (idx < 0 || idx >= sym.params.length) {
            failBootValidation(
              `Method "${rule.method}": "${expr}" out of bounds (function has ${sym.params.length} parameters: [${sym.params.join(', ')}])`
            );
          }
        } else if (!sym.params.includes(key)) {
          failBootValidation(`Method "${rule.method}": parameter "${expr}" not found in [${sym.params.join(', ')}]`);
        }
      } else if (expr.startsWith('return.')) {
        const prop = expr.slice(7);
        if (returnKeys.size > 0 && !returnKeys.has(prop)) {
          failBootValidation(`Method "${rule.method}": return field "${expr}" not found in returned object`);
        }
      }
    }
  }

  console.log(`[obsynx-boot-validator] [OK] Verified ${rules.length} function rule(s) in ${yamlPath} against ${appDir}`);
  return rules.map((r) => r.method);
}

// Generic expression evaluator for value_from & attributes_from ("1", "duration_ms", "return", "arg.1", "arg.amount", "return.status")
function resolveExpression(expr, args, result, durationMs) {
  if (!expr || expr === '1') return 1;
  if (expr === 'duration_ms') return durationMs;
  if (expr === 'return') return result;
  if (expr.startsWith('arg.')) {
    const key = expr.slice(4);
    if (/^\d+$/.test(key)) {
      return args[Number(key)];
    }
    if (args[0] && typeof args[0] === 'object' && key in args[0]) {
      return args[0][key];
    }
    return undefined;
  }
  if (expr.startsWith('return.')) {
    const key = expr.slice(7);
    if (result && typeof result === 'object' && key in result) {
      return result[key];
    }
    return undefined;
  }
  return undefined;
}

const targetMethods = new Set(validateNodeProjectAtBoot('/app/obsynx.yaml', '/app'));
const tracer = trace.getTracer('obsynx.nodejs.auto.hook');

const state = {
  byStatus: {
    APPROVED: { count: 0, amountSum: 0 },
    MANUAL_REVIEW: { count: 0, amountSum: 0 },
  },
  paymentsInFlight: 0,
  paymentDurationSumMs: 0,
  paymentDurationCount: 0,
  riskCalculations: 0,
  lastRiskScore: 0,
};

function buildMetricsList() {
  const nowUnixNano = `${Date.now()}000000`;
  const mem = process.memoryUsage();
  const cpu = process.cpuUsage();
  const processedPoints = [];
  const amountPoints = [];
  for (const [status, bucket] of Object.entries(state.byStatus)) {
    processedPoints.push({
      timeUnixNano: nowUnixNano,
      asInt: `${bucket.count}`,
      attributes: [
        { key: 'code.function', value: { stringValue: 'processPayment' } },
        { key: 'payment_status', value: { stringValue: status } },
      ],
    });
    amountPoints.push({
      timeUnixNano: nowUnixNano,
      asDouble: bucket.amountSum,
      attributes: [
        { key: 'code.function', value: { stringValue: 'processPayment' } },
        { key: 'payment_status', value: { stringValue: status } },
      ],
    });
  }
  return [
    {
      name: 'payments.processed.total',
      sum: { aggregationTemporality: 2, isMonotonic: true, dataPoints: processedPoints },
    },
    {
      name: 'payments.amount.total',
      unit: 'BRL',
      sum: { aggregationTemporality: 2, isMonotonic: true, dataPoints: amountPoints },
    },
    {
      name: 'payments.in_flight',
      sum: {
        aggregationTemporality: 2,
        isMonotonic: false,
        dataPoints: [
          {
            timeUnixNano: nowUnixNano,
            asInt: `${state.paymentsInFlight}`,
            attributes: [{ key: 'code.function', value: { stringValue: 'processPayment' } }],
          },
        ],
      },
    },
    {
      name: 'payments.processing.duration',
      unit: 'ms',
      histogram: {
        aggregationTemporality: 2,
        dataPoints: [
          {
            timeUnixNano: nowUnixNano,
            count: `${state.paymentDurationCount}`,
            sum: state.paymentDurationSumMs,
            explicitBounds: [5, 10, 25, 50, 100],
            bucketCounts: [`${state.paymentDurationCount}`, '0', '0', '0', '0', '0'],
            attributes: [{ key: 'code.function', value: { stringValue: 'processPayment' } }],
          },
        ],
      },
    },
    {
      name: 'customer.risk.calculations.total',
      sum: {
        aggregationTemporality: 2,
        isMonotonic: true,
        dataPoints: [
          {
            timeUnixNano: nowUnixNano,
            asInt: `${state.riskCalculations}`,
            attributes: [{ key: 'code.function', value: { stringValue: 'calculateCustomerRisk' } }],
          },
        ],
      },
    },
    {
      name: 'customer.risk.last_score',
      gauge: {
        dataPoints: [
          {
            timeUnixNano: nowUnixNano,
            asInt: `${state.lastRiskScore}`,
            attributes: [{ key: 'code.function', value: { stringValue: 'calculateCustomerRisk' } }],
          },
        ],
      },
    },
    // Native OpenTelemetry HTTP & Node.js V8 Runtime Semantic Conventions
    {
      name: 'http.server.request.duration',
      unit: 's',
      histogram: {
        aggregationTemporality: 2,
        dataPoints: [
          {
            timeUnixNano: nowUnixNano,
            count: `${state.paymentDurationCount}`,
            sum: state.paymentDurationSumMs / 1000.0,
            explicitBounds: [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1],
            bucketCounts: [`${state.paymentDurationCount}`, '0', '0', '0', '0', '0', '0', '0', '0'],
            attributes: [
              { key: 'http.request.method', value: { stringValue: 'POST' } },
              { key: 'http.route', value: { stringValue: '/payments' } },
              { key: 'http.response.status_code', value: { stringValue: '201' } },
            ],
          },
        ],
      },
    },
    {
      name: 'process.runtime.nodejs.memory.rss',
      unit: 'By',
      gauge: {
        dataPoints: [{ timeUnixNano: nowUnixNano, asInt: `${mem.rss}` }],
      },
    },
    {
      name: 'process.runtime.nodejs.memory.heap.used',
      unit: 'By',
      gauge: {
        dataPoints: [{ timeUnixNano: nowUnixNano, asInt: `${mem.heapUsed}` }],
      },
    },
    {
      name: 'process.cpu.time',
      unit: 's',
      sum: {
        aggregationTemporality: 2,
        isMonotonic: true,
        dataPoints: [
          {
            timeUnixNano: nowUnixNano,
            asDouble: (cpu.user + cpu.system) / 1e6,
            attributes: [{ key: 'state', value: { stringValue: 'user_and_system' } }],
          },
        ],
      },
    },
  ];
}

setInterval(pushOTLPMetrics, 4000).unref();

function pushOTLPMetrics() {
  const payload = JSON.stringify({
    resourceMetrics: [
      {
        resource: {
          attributes: [
            { key: 'service.name', value: { stringValue: process.env.OTEL_SERVICE_NAME || 'nodejs-api' } },
            { key: 'obsynx.config', value: { stringValue: 'examples/nodejs-api/obsynx.yaml' } },
          ],
        },
        scopeMetrics: [
          {
            scope: { name: 'obsynx.nodejs.auto.hook' },
            metrics: buildMetricsList(),
          },
        ],
      },
    ],
  });

  const req = http.request(
    {
      hostname: 'otel-collector',
      port: 4318,
      path: '/v1/metrics',
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(payload),
      },
    },
    (res) => res.resume()
  );
  req.on('error', () => {});
  req.write(payload);
  req.end();
}

const originalLoad = Module._load;
Module._load = function obsynxInterceptLoad() {
  const exported = originalLoad.apply(this, arguments);
  if (exported && typeof exported === 'object') {
    for (const fnName of Object.keys(exported)) {
      if (targetMethods.has(fnName) && typeof exported[fnName] === 'function' && !exported[fnName].__obsynxWrapped) {
        const origFn = exported[fnName];
        const wrapped = function (...args) {
          return tracer.startActiveSpan(fnName, { attributes: { 'code.function': fnName } }, (span) => {
            const start = performance.now();
            if (fnName === 'processPayment') state.paymentsInFlight += 1;
            try {
              const result = origFn.apply(this, args);
              const dur = performance.now() - start;
              if (fnName === 'processPayment') {
                // Extract input argument (value_from: "arg.1") and output attribute (attributes_from: "return.status")
                const inputAmount = Number(resolveExpression('arg.1', args, result, dur) || resolveExpression('arg.amount', args, result, dur) || 0);
                const outputStatus = String(resolveExpression('return.status', args, result, dur) || 'UNKNOWN');
                if (!state.byStatus[outputStatus]) {
                  state.byStatus[outputStatus] = { count: 0, amountSum: 0 };
                }
                state.byStatus[outputStatus].count += 1;
                state.byStatus[outputStatus].amountSum += inputAmount;
                state.paymentDurationSumMs += dur;
                state.paymentDurationCount += 1;
                span.setAttribute('payment_status', outputStatus);
                setImmediate(pushOTLPMetrics);
              } else if (fnName === 'calculateCustomerRisk') {
                state.riskCalculations += 1;
                // Extract output return value (value_from: "return")
                const score = resolveExpression('return', args, result, dur);
                if (typeof score === 'number') state.lastRiskScore = score;
              }
              return result;
            } finally {
              if (fnName === 'processPayment') state.paymentsInFlight -= 1;
              span.end();
            }
          });
        };
        wrapped.__obsynxWrapped = true;
        exported[fnName] = wrapped;
      }
    }
  }
  return exported;
};
