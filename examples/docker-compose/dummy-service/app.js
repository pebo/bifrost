const express = require('express');
const app = express();
const port = 3000;

app.use(express.json());

app.get('/hello/:name', (req, res) => {
  const name = req.params.name;
  res.send(`Hello, ${name}! This is the dummy service.`);
});

app.post('/echo', (req, res) => {
    console.log('Received body:', req.body);
    res.json(req.body);
});

app.listen(port, () => {
  console.log(`Dummy service listening at http://localhost:${port}`);
});
