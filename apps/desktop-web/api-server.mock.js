import http from 'http';

const mockDesktopInfo = {
  desktop_id: 'desktop-dev-001',
  hostname: 'desktop-dev-001.desktops.orchael.dev',
  github_owner: 'myorg',
  environment: 'dev',
  bridge_port: 9445,
  repos: ['repo1', 'repo2'],
  services: [
    { name: 'docker', active: true },
    { name: 'bridge', active: true },
    { name: 'novnc', active: true }
  ],
  novnc_url: 'https://desktop-dev-001.desktops.orchael.dev:8443/novnc/vnc.html'
};

const server = http.createServer((req, res) => {
  res.setHeader('Content-Type', 'application/json');
  res.setHeader('Access-Control-Allow-Origin', '*');

  if (req.url === '/api/desktop' && req.method === 'GET') {
    res.writeHead(200);
    res.end(JSON.stringify(mockDesktopInfo));
  } else {
    res.writeHead(404);
    res.end(JSON.stringify({ error: 'Not found' }));
  }
});

const PORT = 3001;
server.listen(PORT, '127.0.0.1', () => {
  console.log(`Mock API server running on http://127.0.0.1:${PORT}`);
});
