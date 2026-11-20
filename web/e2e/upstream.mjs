import { createServer } from 'node:http'

const server = createServer((req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1:8099')
  res.setHeader('Content-Type', 'application/json; charset=utf-8')
  res.end(JSON.stringify({
    podIp: url.searchParams.get('podIp'),
    showAllPods: url.searchParams.get('showAllPods'),
    instance: { name: 'aquaman-api-6d98c', namespace: 'platform', status: 'Running' },
    data: { list: [{ totalIp: '10.0.0.1' }, { totalIp: '10.0.0.2' }] },
  }))
})
server.listen(8099, '127.0.0.1')
process.on('SIGTERM', () => server.close())
process.on('SIGINT', () => server.close())
