FROM gradle:8.10.2-jdk21-alpine AS builder
WORKDIR /workspace
COPY build.gradle.kts settings.gradle.kts ./
COPY src ./src
RUN gradle bootJar --no-daemon

FROM eclipse-temurin:21-jre-alpine
WORKDIR /app
COPY --from=builder /workspace/build/libs/*.jar /app/app.jar
COPY obsynx.yaml /app/obsynx.yaml
EXPOSE 8080
ENTRYPOINT ["java", "-jar", "/app/app.jar"]
