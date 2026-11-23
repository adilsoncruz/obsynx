import os
import time
from flask import Flask, jsonify, request

app = Flask(__name__)


def calculate_customer_risk(account_id: str, amount: float) -> int:
    time.sleep(0.01)
    if amount > 1000.0:
        return 85
    if not account_id:
        return 50
    return 15


def process_payment(account_id: str, amount: float, risk_score: int) -> dict:
    time.sleep(0.015)
    status = "MANUAL_REVIEW" if risk_score >= 80 else "APPROVED"
    return {
        "transaction_id": f"tx-py-{int(time.time() * 1000)}",
        "account_id": account_id,
        "amount": amount,
        "risk_score": risk_score,
        "status": status,
    }


@app.get("/health")
def health():
    return jsonify({"status": "UP", "service": "python-api"}), 200


@app.post("/payments")
def payments():
    payload = request.get_json(silent=True) or {}
    account_id = str(payload.get("account_id", ""))
    amount = float(payload.get("amount", 0.0))
    risk = calculate_customer_risk(account_id, amount)
    result = process_payment(account_id, amount, risk)
    return jsonify(result), 201


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "8083"))
    app.run(host="0.0.0.0", port=port)
