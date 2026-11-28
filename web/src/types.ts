export interface User {
  id: number;
  username: string;
  role: "admin" | "member";
  disabled: boolean;
}

export interface Parameter {
  name: string;
  description: string;
  in: "query" | "path" | "header" | "body";
  type: "string" | "boolean" | "integer" | "number" | "object" | "array";
  required: boolean;
  queryFilter?: boolean;
  nonEmpty?: boolean;
  fixed: boolean;
  value: string;
}

export interface GrafanaQuery {
  refId: string;
  datasourceUid: string;
  datasourceType: string;
  datasourceName?: string;
  expr: string;
  queryMode?: string;
  maxDataPoints?: number;
  interval?: string;
  maxLines?: number;
  timeField?: string;
}

export interface GrafanaConfig {
  connectionId: number;
  fromRequired: boolean;
  defaultFrom?: string;
  toRequired: boolean;
  defaultTo?: string;
  queries: GrafanaQuery[];
}

export interface GrafanaConnection {
  id: number;
  name: string;
  baseUrl: string;
  authType: "bearer" | "basic" | "cookie";
  username?: string;
  createdAt: string;
  updatedAt: string;
}

export interface GrafanaDatasource {
  uid: string;
  name: string;
  type: string;
  isDefault?: boolean;
}

export interface Definition {
  kind?: "http" | "grafana";
  name: string;
  description: string;
  url: string;
  method: string;
  timeoutSeconds: number;
  bodyTemplate?: string;
  grafana?: GrafanaConfig;
  parameters: Parameter[];
  outputFields?: OutputField[];
  approval?: ApprovalRule;
}

export interface ApprovalRule {
  approver: "self" | "admin" | "user";
  approverName?: string;
}

export interface Approval {
  id: number;
  toolId: number | null;
  toolName: string;
  method?: string;
  description?: string;
  requester: string;
  approver: string;
  status: "pending" | "expired" | "rejected" | "executing" | "executed" | "upstream_error" | "unknown" | "failed";
  canDecide: boolean;
  decidedBy?: string;
  createdAt: string;
  expiresAt: string;
  decidedAt?: string;
  finishedAt?: string;
  arguments?: unknown;
  request?: { method: string; url: string; body?: string };
  result?: CallResult;
  error?: string;
}

export interface OutputField {
  name: string;
  source: "response" | "fixed";
  value: string;
}

export interface Tool extends Definition {
  id: number;
  ownerId: number;
  ownerName: string;
  published: boolean;
  public: boolean;
  updatedAt: string;
}

export interface AccessToken {
  id: number;
  name: string;
  prefix: string;
  createdAt: string;
}

export interface CallResult {
  statusCode: number;
  body: string;
  rawBody?: string;
  contentType: string;
  durationMs: number;
  outputMapped?: boolean;
}

export type GrafanaDatasourceFamily =
  | "prometheus"
  | "loki"
  | "victorialogs"
  | "clickhouse"
  | "sql"
  | "elasticsearch"
  | "generic";

export function grafanaDatasourceFamily(dsType: string): GrafanaDatasourceFamily {
  const t = (dsType || "").trim().toLowerCase();
  if (t.includes("victoriametrics-logs") || t.includes("victorialogs") || t.includes("vmlogs")) {
    return "victorialogs";
  }
  if (t.includes("loki")) return "loki";
  if (t.includes("prometheus") || t.includes("victoriametrics")) return "prometheus";
  if (t.includes("clickhouse")) return "clickhouse";
  if (
    t.includes("mysql") ||
    t.includes("postgres") ||
    t.includes("mssql") ||
    t.includes("sqlite") ||
    t.includes("trino") ||
    t.includes("oracle") ||
    t.includes("snowflake")
  ) {
    return "sql";
  }
  if (t.includes("elasticsearch") || t.includes("opensearch")) return "elasticsearch";
  return "generic";
}

export function emptyGrafanaQuery(refId = "A", previous?: GrafanaQuery): GrafanaQuery {
  return {
    refId,
    datasourceUid: previous?.datasourceUid ?? "",
    datasourceType: previous?.datasourceType ?? "",
    datasourceName: previous?.datasourceName ?? "",
    expr: "",
    queryMode: "",
    maxDataPoints: undefined,
    interval: "",
    maxLines: undefined,
    timeField: "",
  };
}

export const emptyDefinition = (): Definition => ({
  name: "",
  description: "",
  url: "",
  method: "GET",
  timeoutSeconds: 15,
  parameters: [],
});

export const emptyGrafanaDefinition = (connectionId = 0): Definition => ({
  kind: "grafana",
  name: "",
  description: "",
  url: "",
  method: "",
  timeoutSeconds: 15,
  grafana: {
    connectionId,
    fromRequired: false,
    defaultFrom: "now-1h",
    toRequired: false,
    defaultTo: "now",
    queries: [emptyGrafanaQuery("A")],
  },
  parameters: [],
});

export const podExample = (): Definition => ({
  name: "getPodInstance",
  description: "按 Pod IP 查询 Kubernetes 实例，固定不查询全部 Pod。",
  url: "http://10.12.72.42:9007/ws/aquaman/kubernetes/pods/getPodInstance",
  method: "GET",
  timeoutSeconds: 15,
  parameters: [
    {
      name: "showAllPods",
      description: "固定不查询全部 Pod",
      in: "query",
      type: "boolean",
      required: false,
      fixed: true,
      value: "false",
    },
    {
      name: "podIp",
      description: "要查询的 Pod IP，例如 10.60.144.183",
      in: "query",
      type: "string",
      required: true,
      fixed: false,
      value: "",
    },
  ],
});

export function definitionOf(d: Definition): Definition {
  const isGrafana = d.kind === "grafana";
  return {
    kind: isGrafana ? "grafana" : undefined,
    name: d.name,
    description: d.description,
    url: d.url ?? "",
    method: d.method ?? (isGrafana ? "" : "GET"),
    timeoutSeconds: d.timeoutSeconds,
    bodyTemplate: d.bodyTemplate,
    grafana: isGrafana
      ? {
          connectionId: d.grafana?.connectionId ?? 0,
          fromRequired: d.grafana?.fromRequired ?? false,
          defaultFrom: d.grafana?.defaultFrom ?? (d.grafana?.fromRequired ? "" : "now-1h"),
          toRequired: d.grafana?.toRequired ?? false,
          defaultTo: d.grafana?.defaultTo ?? (d.grafana?.toRequired ? "" : "now"),
          queries: d.grafana?.queries?.length
            ? d.grafana.queries.map((q) => ({
                ...q,
                queryMode: q.queryMode ?? "",
                interval: q.interval ?? "",
                timeField: q.timeField ?? "",
              }))
            : [emptyGrafanaQuery("A")],
        }
      : undefined,
    parameters: (d.parameters ?? []).map((p) => ({ ...p })),
    outputFields: d.outputFields?.length ? d.outputFields.map((field) => ({ ...field })) : undefined,
    approval: d.approval ? { ...d.approval } : undefined,
  };
}
