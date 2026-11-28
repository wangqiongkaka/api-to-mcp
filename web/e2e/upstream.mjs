import { createServer } from 'node:http'

const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1:8099')
  res.setHeader('Content-Type', 'application/json; charset=utf-8')
  if (url.pathname === '/api/datasources' || url.pathname === '/api/ds/query') {
    const auth = req.headers.authorization || ''
    const cookie = req.headers.cookie || ''
    const ok = auth === 'Bearer glsa_e2e_token' || auth === `Basic ${Buffer.from('ops:ops-pass-123').toString('base64')}` || cookie === 'grafana_session=e2e_ok'
    if (!ok) {
      res.statusCode = 401
      res.end(JSON.stringify({ message: 'Unauthorized' }))
      return
    }
    if (url.pathname === '/api/datasources') {
      res.end(JSON.stringify([
        { uid: 'prom-main', name: 'Prometheus Main', type: 'prometheus', isDefault: true },
        { uid: 'vmlogs-main', name: 'VictoriaLogs Main', type: 'victoriametrics-logs-datasource' },
        { uid: 'ch-main', name: 'ClickHouse Main', type: 'grafana-clickhouse-datasource' },
      ]))
      return
    }
    const chunks = []
    for await (const chunk of req) chunks.push(chunk)
    const body = JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}')
    const results = {}
    for (const q of body.queries || []) {
      const statement = q.expr || q.rawSql || q.query || ''
      if ((q.datasource?.type || '').includes('prometheus')) {
        // 300 分钟时序数据，在 2026-05-21T08:00:00+08:00（1779321600000）处发生骤降 83.92 -> 43.95
        const dropMs = 1779321600000
        const startMs = dropMs - 180 * 60000
        const ts = []
        const vals = []
        for (let i = 0; i < 300; i++) {
          ts.push(startMs + i * 60000)
          if (i < 179) vals.push(84)
          else if (i === 179) vals.push(83.92)
          else if (i === 180) vals.push(43.95)
          else vals.push(43.1)
        }
        results[q.refId] = {
          status: 200,
          frames: [{
            schema: {
              name: q.refId,
              refId: q.refId,
              fields: [
                { name: 'Time', type: 'time' },
                { name: 'Value', type: 'number', labels: { datasource: q.datasource?.type || '', expr: statement, range: `${body.from}..${body.to}` } },
              ],
            },
            data: {
              values: [ts, vals],
            },
          }],
        }
        continue
      }
      const fields = [
        { name: 'Time', type: 'time' },
        { name: 'Statement', type: 'string', labels: { datasource: q.datasource?.type || '' } },
        { name: 'Range', type: 'string' },
      ]
      const values = [
        [1700000000000],
        [statement],
        [`${body.from}..${body.to}`],
      ]
      if ((q.datasource?.type || '').includes('logs')) {
        fields.push(
          { name: '_msg', type: 'string' },
          { name: 'pod_uid', type: 'string' },
        )
        values.push(
          ['10.20.30.40 [ERROR] {"trace_id":"tr-e2e-9","events":[{"code":"E1","msg":"db timeout","debug_noise":"huge"},{"code":"E2","msg":"retry fail","debug_noise":"huge"}]}'],
          ['redundant-pod-uid'],
        )
      }
      results[q.refId] = {
        status: 200,
        frames: [{
          schema: {
            name: q.refId,
            refId: q.refId,
            fields,
          },
          data: {
            values,
          },
        }],
      }
    }
    res.end(JSON.stringify({ results }))
    return
  }
  if (req.method === 'POST' && url.pathname === '/items') {
    for await (const chunk of req) res.write(chunk)
    res.end()
    return
  }
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
