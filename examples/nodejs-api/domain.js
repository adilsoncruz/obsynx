function calculateCustomerRisk(accountId, amount) {
  if (amount > 1000) return 85;
  if (!accountId) return 50;
  return 15;
}

function processPayment(accountId, amount, riskScore) {
  const status = riskScore >= 80 ? 'MANUAL_REVIEW' : 'APPROVED';
  return {
    transaction_id: `tx-node-${Date.now()}`,
    account_id: accountId,
    amount,
    risk_score: riskScore,
    status,
  };
}

module.exports = {
  calculateCustomerRisk,
  processPayment,
};
