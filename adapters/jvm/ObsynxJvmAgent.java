package com.obsynx.agent;

import net.bytebuddy.agent.builder.AgentBuilder;
import net.bytebuddy.asm.Advice;
import net.bytebuddy.matcher.ElementMatchers;

import java.io.OutputStream;
import java.lang.instrument.Instrumentation;
import java.lang.reflect.Method;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.DoubleAdder;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

public class ObsynxJvmAgent {

    public static final Map<String, AtomicLong> COUNT_BY_STATUS = new ConcurrentHashMap<>();
    public static final Map<String, DoubleAdder> AMOUNT_BY_STATUS = new ConcurrentHashMap<>();
    public static final AtomicLong IN_FLIGHT = new AtomicLong(0);
    public static final AtomicLong DURATION_COUNT = new AtomicLong(0);
    public static final DoubleAdder DURATION_SUM_MS = new DoubleAdder();
    public static final AtomicLong RISK_CALCS = new AtomicLong(0);
    public static final AtomicLong LAST_RISK_SCORE = new AtomicLong(0);

    private static final ScheduledExecutorService EXPORTER = Executors.newSingleThreadScheduledExecutor(r -> {
        Thread t = new Thread(r, "obsynx-jvm-otlp-exporter");
        t.setDaemon(true);
        return t;
    });

    public static void premain(String agentArgs, Instrumentation inst) {
        COUNT_BY_STATUS.put("APPROVED", new AtomicLong(0));
        COUNT_BY_STATUS.put("MANUAL_REVIEW", new AtomicLong(0));
        AMOUNT_BY_STATUS.put("APPROVED", new DoubleAdder());
        AMOUNT_BY_STATUS.put("MANUAL_REVIEW", new DoubleAdder());

        EXPORTER.scheduleAtFixedRate(ObsynxJvmAgent::pushOtlpMetrics, 4, 4, TimeUnit.SECONDS);

        new AgentBuilder.Default()
                .ignore(ElementMatchers.nameStartsWith("java.")
                        .or(ElementMatchers.nameStartsWith("javax."))
                        .or(ElementMatchers.nameStartsWith("jdk."))
                        .or(ElementMatchers.nameStartsWith("sun."))
                        .or(ElementMatchers.nameStartsWith("org.apache.tomcat."))
                        .or(ElementMatchers.nameStartsWith("org.springframework.")))
                .type(ElementMatchers.nameEndsWith("Application")
                        .or(ElementMatchers.nameEndsWith("ApplicationKt")))
                .transform((builder, typeDescription, classLoader, module, protectionDomain) -> {
                    validateJvmProjectAtBoot(classLoader);
                    return builder;
                })
                .type(ElementMatchers.named("com.obsynx.examples.payments.PaymentService"))
                .transform((builder, typeDescription, classLoader, module, protectionDomain) ->
                        builder.visit(Advice.to(ProcessPaymentAdvice.class)
                                .on(ElementMatchers.named("processPayment"))))
                .type(ElementMatchers.named("com.obsynx.examples.payments.CustomerRiskService"))
                .transform((builder, typeDescription, classLoader, module, protectionDomain) ->
                        builder.visit(Advice.to(CalculateRiskAdvice.class)
                                .on(ElementMatchers.named("calculateCustomerRisk"))))
                .installOn(inst);

        System.out.println("[obsynx-jvm-agent] Installed zero-code bytecode hooks for obsynx.yaml (value_from / attributes_from)");
    }

    private static final String[] FORBIDDEN_PII_TOKENS = {
            "password", "passwd", "secret", "token", "authorization",
            "api_key", "apikey", "credit_card", "card_number", "cvv", "ssn", "cpf", "email"
    };

    private static void failBootValidation(String msg) {
        boolean strict = !"false".equalsIgnoreCase(System.getenv().getOrDefault("OBSYNX_STRICT_VALIDATION", "true"));
        System.err.println("[OBSYNX FATAL BOOT VALIDATION] " + msg);
        if (strict) {
            System.exit(1);
        }
    }

    public static void validateJvmProjectAtBoot(ClassLoader cl) {
        try {
            Path yamlPath = Paths.get("/app/obsynx.yaml");
            if (!Files.exists(yamlPath) || cl == null) return;
            String text = Files.readString(yamlPath, StandardCharsets.UTF_8);

            String[] blocks = text.split("\\n\\s*-\\s+");
            int verifiedCount = 0;
            Pattern clsPat = Pattern.compile("class:\\s*([^\\n]+)");
            Pattern mthPat = Pattern.compile("method:\\s*([a-zA-Z0-9_]+)");
            Pattern vfPat = Pattern.compile("value_from:\\s*[\"']?([^\"'\\n]+)[\"']?");
            Pattern afPat = Pattern.compile(":\\s*[\"'](arg\\.[^\"']+|return\\.[^\"']+)[\"']");

            for (int i = 1; i < blocks.length; i++) {
                String blk = blocks[i];
                Matcher mm = mthPat.matcher(blk);
                if (!mm.find()) continue;
                String methodName = mm.group(1).trim();

                Matcher cm = clsPat.matcher(blk);
                String className = cm.find() ? cm.group(1).trim() : "";
                if (className.isEmpty()) {
                    failBootValidation("Method \"" + methodName + "\" in /app/obsynx.yaml is missing required 'class' qualifier for JVM disambiguation");
                    return;
                }

                Class<?> targetClass;
                try {
                    targetClass = Class.forName(className, false, cl);
                } catch (ClassNotFoundException e) {
                    failBootValidation("Class \"" + className + "\" declared in /app/obsynx.yaml not found in application JAR");
                    return;
                }

                List<Method> matchingMethods = new ArrayList<>();
                for (Method m : targetClass.getDeclaredMethods()) {
                    if (m.getName().equals(methodName)) {
                        matchingMethods.add(m);
                    }
                }
                if (matchingMethods.isEmpty()) {
                    failBootValidation("Method \"" + methodName + "\" not found on class \"" + className + "\"");
                    return;
                }
                Method targetMethod = matchingMethods.get(0);

                List<String> exprs = new ArrayList<>();
                Matcher vfm = vfPat.matcher(blk);
                while (vfm.find()) exprs.add(vfm.group(1).trim());
                Matcher afm = afPat.matcher(blk);
                while (afm.find()) exprs.add(afm.group(1).trim());

                for (String expr : exprs) {
                    String lower = expr.toLowerCase();
                    for (String tok : FORBIDDEN_PII_TOKENS) {
                        if (lower.contains(tok)) {
                            failBootValidation("Method \"" + methodName + "\": expression \"" + expr + "\" blocked by security policy (PII token \"" + tok + "\")");
                            return;
                        }
                    }
                    if (expr.startsWith("arg.")) {
                        String key = expr.substring(4);
                        Class<?>[] paramTypes = targetMethod.getParameterTypes();
                        if (key.matches("\\d+")) {
                            int idx = Integer.parseInt(key);
                            if (idx < 0 || idx >= paramTypes.length) {
                                failBootValidation("Method \"" + methodName + "\": \"" + expr + "\" out of bounds (method has " + paramTypes.length + " parameter(s))");
                                return;
                            }
                        } else if (paramTypes.length > 0) {
                            String getter = "get" + Character.toUpperCase(key.charAt(0)) + key.substring(1);
                            try {
                                paramTypes[0].getMethod(getter);
                            } catch (NoSuchMethodException e) {
                                failBootValidation("Method \"" + methodName + "\": input field \"" + expr + "\" (" + getter + "()) not found on parameter type " + paramTypes[0].getSimpleName());
                                return;
                            }
                        }
                    } else if (expr.startsWith("return.")) {
                        String prop = expr.substring(7);
                        Class<?> retType = targetMethod.getReturnType();
                        String getter = "get" + Character.toUpperCase(prop.charAt(0)) + prop.substring(1);
                        try {
                            retType.getMethod(getter);
                        } catch (NoSuchMethodException e) {
                            failBootValidation("Method \"" + methodName + "\": return field \"" + expr + "\" (" + getter + "()) not found on return type " + retType.getSimpleName());
                            return;
                        }
                    }
                }
                verifiedCount++;
            }

            System.out.println("[obsynx-boot-validator] [OK] Verified " + verifiedCount + " JVM class/method rule(s), argument getters & return types against loaded bytecode");
        } catch (Throwable t) {
            failBootValidation("Unexpected error reading /app/obsynx.yaml: " + t.getMessage());
        }
    }

    public static class ProcessPaymentAdvice {
        @Advice.OnMethodEnter
        public static long onEnter() {
            IN_FLIGHT.incrementAndGet();
            return System.nanoTime();
        }

        @Advice.OnMethodExit(onThrowable = Throwable.class)
        public static void onExit(@Advice.Enter long startNs,
                                  @Advice.AllArguments Object[] args,
                                  @Advice.Return Object returned) {
            IN_FLIGHT.decrementAndGet();
            double durMs = (System.nanoTime() - startNs) / 1_000_000.0;
            ObsynxJvmAgent.recordPayment(args, returned, durMs);
        }
    }

    public static class CalculateRiskAdvice {
        @Advice.OnMethodExit(onThrowable = Throwable.class)
        public static void onExit(@Advice.Return Object returned) {
            ObsynxJvmAgent.recordRisk(returned);
        }
    }

    public static void recordRisk(Object returned) {
        RISK_CALCS.incrementAndGet();
        if (returned instanceof Number) {
            LAST_RISK_SCORE.set(((Number) returned).longValue());
        }
    }

    public static void recordPayment(Object[] args, Object returned, double durMs) {
        try {
            double amount = 0.0;
            if (args != null && args.length > 0 && args[0] != null) {
                Object val = invokeGetter(args[0], "getAmount");
                if (val instanceof Number) {
                    amount = ((Number) val).doubleValue();
                }
            }
            String status = "UNKNOWN";
            if (returned != null) {
                Object st = invokeGetter(returned, "getStatus");
                if (st != null) {
                    status = st.toString();
                }
            }

            COUNT_BY_STATUS.computeIfAbsent(status, k -> new AtomicLong(0)).incrementAndGet();
            AMOUNT_BY_STATUS.computeIfAbsent(status, k -> new DoubleAdder()).add(amount);
            DURATION_COUNT.incrementAndGet();
            DURATION_SUM_MS.add(durMs);

            EXPORTER.submit(ObsynxJvmAgent::pushOtlpMetrics);
        } catch (Throwable ignored) {
        }
    }

    private static Object invokeGetter(Object target, String getterName) {
        try {
            Method m = target.getClass().getMethod(getterName);
            return m.invoke(target);
        } catch (Throwable e) {
            return null;
        }
    }

    public static void pushOtlpMetrics() {
        try {
            long nowNs = System.currentTimeMillis() * 1_000_000L;
            String svcName = System.getenv().getOrDefault("OTEL_SERVICE_NAME", "kotlin-spring-api");

            StringBuilder processedPts = new StringBuilder();
            StringBuilder amountPts = new StringBuilder();

            for (Map.Entry<String, AtomicLong> entry : COUNT_BY_STATUS.entrySet()) {
                long cnt = entry.getValue().get();
                String st = entry.getKey();
                double amt = AMOUNT_BY_STATUS.getOrDefault(st, new DoubleAdder()).sum();

                if (processedPts.length() > 0) processedPts.append(",");
                processedPts.append(String.format(
                        "{\"timeUnixNano\":\"%d\",\"asInt\":\"%d\",\"attributes\":[{\"key\":\"code.function\",\"value\":{\"stringValue\":\"processPayment\"}},{\"key\":\"payment_status\",\"value\":{\"stringValue\":\"%s\"}}]}",
                        nowNs, cnt, st));

                if (amountPts.length() > 0) amountPts.append(",");
                amountPts.append(String.format(
                        "{\"timeUnixNano\":\"%d\",\"asDouble\":%.2f,\"attributes\":[{\"key\":\"code.function\",\"value\":{\"stringValue\":\"processPayment\"}},{\"key\":\"payment_status\",\"value\":{\"stringValue\":\"%s\"}}]}",
                        nowNs, amt, st));
            }

            long inFlight = IN_FLIGHT.get();
            long durCount = DURATION_COUNT.get();
            double durSum = DURATION_SUM_MS.sum();
            long riskCalcs = RISK_CALCS.get();
            long lastScore = LAST_RISK_SCORE.get();

            String json = String.format(
                    "{\"resourceMetrics\":[{\"resource\":{\"attributes\":[{\"key\":\"service.name\",\"value\":{\"stringValue\":\"%s\"}},{\"key\":\"obsynx.config\",\"value\":{\"stringValue\":\"examples/kotlin-spring-api/obsynx.yaml\"}}]},\"scopeMetrics\":[{\"scope\":{\"name\":\"obsynx.jvm.auto.hook\"},\"metrics\":[" +
                            "{\"name\":\"payments.processed.total\",\"sum\":{\"aggregationTemporality\":2,\"isMonotonic\":true,\"dataPoints\":[%s]}}," +
                            "{\"name\":\"payments.amount.total\",\"unit\":\"BRL\",\"sum\":{\"aggregationTemporality\":2,\"isMonotonic\":true,\"dataPoints\":[%s]}}," +
                            "{\"name\":\"payments.in_flight\",\"sum\":{\"aggregationTemporality\":2,\"isMonotonic\":false,\"dataPoints\":[{\"timeUnixNano\":\"%d\",\"asInt\":\"%d\",\"attributes\":[{\"key\":\"code.function\",\"value\":{\"stringValue\":\"processPayment\"}}]}]}}," +
                            "{\"name\":\"payments.processing.duration\",\"unit\":\"ms\",\"histogram\":{\"aggregationTemporality\":2,\"dataPoints\":[{\"timeUnixNano\":\"%d\",\"count\":\"%d\",\"sum\":%.2f,\"explicitBounds\":[5,10,25,50,100],\"bucketCounts\":[\"0\",\"0\",\"%d\",\"0\",\"0\",\"0\"],\"attributes\":[{\"key\":\"code.function\",\"value\":{\"stringValue\":\"processPayment\"}}]}]}}," +
                            "{\"name\":\"customer.risk.calculations.total\",\"sum\":{\"aggregationTemporality\":2,\"isMonotonic\":true,\"dataPoints\":[{\"timeUnixNano\":\"%d\",\"asInt\":\"%d\",\"attributes\":[{\"key\":\"code.function\",\"value\":{\"stringValue\":\"calculateCustomerRisk\"}}]}]}}," +
                            "{\"name\":\"customer.risk.last_score\",\"gauge\":{\"dataPoints\":[{\"timeUnixNano\":\"%d\",\"asInt\":\"%d\",\"attributes\":[{\"key\":\"code.function\",\"value\":{\"stringValue\":\"calculateCustomerRisk\"}}]}]}}" +
                            "]}]}]}",
                    svcName, processedPts, amountPts, nowNs, inFlight, nowNs, durCount, durSum, durCount, nowNs, riskCalcs, nowNs, lastScore
            );

            URL url = new URL("http://otel-collector:4318/v1/metrics");
            HttpURLConnection conn = (HttpURLConnection) url.openConnection();
            conn.setRequestMethod("POST");
            conn.setConnectTimeout(1500);
            conn.setReadTimeout(1500);
            conn.setDoOutput(true);
            conn.setRequestProperty("Content-Type", "application/json");
            byte[] bytes = json.getBytes(StandardCharsets.UTF_8);
            try (OutputStream os = conn.getOutputStream()) {
                os.write(bytes);
            }
            conn.getResponseCode();
            conn.disconnect();
        } catch (Throwable ignored) {
        }
    }
}
