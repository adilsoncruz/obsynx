FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod main.go obsynx.yaml ./
# Keep DWARF symbols (-ldflags="" without -s -w) so OBI/eBPF can inspect symbols
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/go-api .

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /out/go-api /app/go-api
COPY obsynx.yaml /app/obsynx.yaml
EXPOSE 8080
ENTRYPOINT ["/app/go-api"]
