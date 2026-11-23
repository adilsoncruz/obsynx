package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"
)

type PaymentRequest struct {
	AccountID string  `json:"account_id"`
	Amount    float64 `json:"amount"`
}

type PaymentResponse struct {
	TransactionID string  `json:"transaction_id"`
	AccountID     string  `json:"account_id"`
	Amount        float64 `json:"amount"`
	RiskScore     int     `json:"risk_score"`
	Status        string  `json:"status"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("POST /payments", handlePayments)

	log.Printf("go-api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"UP","service":"go-api"}`))
}

func handlePayments(w http.ResponseWriter, r *http.Request) {
	var req PaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid payload"}`, http.StatusBadRequest)
		return
	}

	risk := calculateCustomerRisk(req.AccountID, req.Amount)
	resp := processPayment(req, risk)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func calculateCustomerRisk(accountID string, amount float64) int {
	time.Sleep(time.Duration(5+rand.Intn(15)) * time.Millisecond)
	if amount > 1000 {
		return 85
	}
	if accountID == "" {
		return 50
	}
	return 15
}

func processPayment(req PaymentRequest, riskScore int) PaymentResponse {
	time.Sleep(time.Duration(10+rand.Intn(25)) * time.Millisecond)
	status := "APPROVED"
	if riskScore >= 80 {
		status = "MANUAL_REVIEW"
	}
	return PaymentResponse{
		TransactionID: "tx-go-" + time.Now().Format("20060102150405"),
		AccountID:     req.AccountID,
		Amount:        req.Amount,
		RiskScore:     riskScore,
		Status:        status,
	}
}
