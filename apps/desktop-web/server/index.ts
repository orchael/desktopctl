import express from 'express';
import path from 'path';
import { fileURLToPath } from 'url';
import { buildDesktopInfo } from './desktop.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const app = express();
const HOST = '127.0.0.1';
const PORT = parseInt(process.env.PORT ?? '3000', 10);

const distPath = path.join(__dirname, '..', 'dist');
app.use(express.static(distPath));

app.get('/api/desktop', (req, res) => {
  try {
    const forwardedHost = req.headers['x-forwarded-host'];
    const requestHost =
      (Array.isArray(forwardedHost) ? forwardedHost[0] : forwardedHost) ||
      req.headers.host;
    const data = buildDesktopInfo(requestHost);
    res.json(data);
  } catch (err) {
    res.status(500).json({ error: String(err) });
  }
});

app.get('*', (_req, res) => {
  res.sendFile(path.join(distPath, 'index.html'));
});

app.listen(PORT, HOST, () => {
  // eslint-disable-next-line no-console
  console.log(`desktop-web listening on http://${HOST}:${PORT}`);
});
