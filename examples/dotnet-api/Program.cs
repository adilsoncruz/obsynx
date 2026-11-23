var builder = WebApplication.CreateBuilder(args);
builder.WebHost.UseUrls("http://0.0.0.0:5000");

var app = builder.Build();

var paymentService = new PaymentService();

app.MapGet("/health", () => Results.Ok(new { status = "healthy", runtime = "dotnet-8.0" }));

app.MapPost("/payments/process", (PaymentRequest request) =>
{
    var response = paymentService.ProcessPayment(request.Amount, request.Method);
    return Results.Ok(response);
});

app.Run();

// Pure Business Logic Record & Service (Zero OpenTelemetry references)
public record PaymentRequest(decimal Amount, string Method);
public record PaymentResponse(string Status, decimal Amount, string Method, string TransactionId);

public class PaymentService
{
    public PaymentResponse ProcessPayment(decimal amount, string method)
    {
        // Business logic execution
        var transactionId = "TXN-" + Guid.NewGuid().ToString()[..8].ToUpper();
        return new PaymentResponse("APPROVED", amount, method, transactionId);
    }
}
