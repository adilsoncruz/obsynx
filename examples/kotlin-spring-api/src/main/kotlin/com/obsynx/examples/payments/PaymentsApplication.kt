package com.obsynx.examples.payments

import org.springframework.boot.CommandLineRunner
import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.runApplication
import org.springframework.context.annotation.Bean
import org.springframework.http.HttpStatus
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.stereotype.Service
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.util.UUID

@SpringBootApplication
class PaymentsApplication {
    @Bean
    fun initSchema(jdbcTemplate: JdbcTemplate) = CommandLineRunner {
        jdbcTemplate.execute(
            """
            CREATE TABLE IF NOT EXISTS payments (
                transaction_id VARCHAR(64) PRIMARY KEY,
                account_id VARCHAR(64) NOT NULL,
                amount DOUBLE NOT NULL,
                risk_score INT NOT NULL,
                status VARCHAR(32) NOT NULL
            )
            """.trimIndent()
        )
    }
}

fun main(args: Array<String>) {
    runApplication<PaymentsApplication>(*args)
}

data class PaymentRequest(
    val accountId: String,
    val amount: Double
)

data class PaymentResponse(
    val transactionId: String,
    val accountId: String,
    val amount: Double,
    val riskScore: Int,
    val status: String
)

@Service
class CustomerRiskService {
    fun calculateCustomerRisk(accountId: String, amount: Double): Int {
        Thread.sleep(12)
        return when {
            amount > 1000.0 -> 85
            accountId.isBlank() -> 50
            else -> 15
        }
    }
}

@Service
class PaymentService(
    private val jdbcTemplate: JdbcTemplate,
    private val customerRiskService: CustomerRiskService
) {
    fun processPayment(request: PaymentRequest): PaymentResponse {
        val riskScore = customerRiskService.calculateCustomerRisk(request.accountId, request.amount)
        val status = if (riskScore >= 80) "MANUAL_REVIEW" else "APPROVED"
        val txId = "tx-jvm-${UUID.randomUUID().toString().take(8)}"

        jdbcTemplate.update(
            "INSERT INTO payments (transaction_id, account_id, amount, risk_score, status) VALUES (?, ?, ?, ?, ?)",
            txId,
            request.accountId,
            request.amount,
            riskScore,
            status
        )

        return PaymentResponse(
            transactionId = txId,
            accountId = request.accountId,
            amount = request.amount,
            riskScore = riskScore,
            status = status
        )
    }
}

@RestController
class PaymentController(private val paymentService: PaymentService) {

    @GetMapping("/health")
    fun health(): Map<String, String> = mapOf("status" to "UP", "service" to "kotlin-spring-api")

    @PostMapping("/payments")
    @ResponseStatus(HttpStatus.CREATED)
    fun createPayment(@RequestBody request: PaymentRequest): PaymentResponse {
        return paymentService.processPayment(request)
    }
}
