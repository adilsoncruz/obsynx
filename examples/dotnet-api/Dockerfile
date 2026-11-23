# Multi-stage Dockerfile for Pure .NET 8 API (Zero OpenTelemetry in final runtime)
FROM mcr.microsoft.com/dotnet/sdk:8.0 AS build
WORKDIR /src
COPY PaymentsApi.csproj .
RUN dotnet restore PaymentsApi.csproj
COPY Program.cs .
RUN dotnet publish PaymentsApi.csproj -c Release -o /app/publish /p:UseAppHost=false

FROM mcr.microsoft.com/dotnet/aspnet:8.0-alpine AS final
WORKDIR /app
COPY --from=build /app/publish .
COPY obsynx.yaml /app/obsynx.yaml

EXPOSE 5000
ENTRYPOINT ["dotnet", "PaymentsApi.dll"]
