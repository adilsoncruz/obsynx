const express = require('express');
const domain = require('./domain');

const app = express();
app.use(express.json());

app.get('/health', (_req, res) => {
  res.json({ status: 'UP', service: 'nodejs-api' });
});

app.post('/payments', (req, res) => {
  const { account_id = '', amount = 0 } = req.body || {};
  const riskScore = domain.calculateCustomerRisk(account_id, Number(amount));
  const result = domain.processPayment(account_id, Number(amount), riskScore);
  res.status(201).json(result);
});

const port = process.env.PORT || 8082;
app.listen(port, () => {
  console.log(`nodejs-api listening on :${port}`);
});
