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

export interface Definition {
  name: string;
  description: string;
  url: string;
  method: string;
  timeoutSeconds: number;
  bodyTemplate?: string;
  parameters: Parameter[];
  outputFields?: OutputField[];
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
  contentType: string;
  durationMs: number;
  outputMapped?: boolean;
}

export const emptyDefinition = (): Definition => ({
  name: "",
  description: "",
  url: "",
  method: "GET",
  timeoutSeconds: 15,
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
  return {
    name: d.name,
    description: d.description,
    url: d.url,
    method: d.method,
    timeoutSeconds: d.timeoutSeconds,
    bodyTemplate: d.bodyTemplate,
    parameters: d.parameters.map((p) => ({ ...p })),
    outputFields: d.outputFields?.length ? d.outputFields.map((field) => ({ ...field })) : undefined,
  };
}
