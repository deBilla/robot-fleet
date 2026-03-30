const API_KEY_STORAGE_KEY = 'fleetos_admin_api_key';

function getApiKey(): string {
  return localStorage.getItem(API_KEY_STORAGE_KEY) || 'dev-key-001';
}

export function setApiKey(key: string): void {
  localStorage.setItem(API_KEY_STORAGE_KEY, key);
}

export interface ApiResponse<T = unknown> {
  ok: boolean;
  status: number;
  data: T | null;
}

async function request<T = unknown>(method: string, path: string, body?: unknown): Promise<ApiResponse<T>> {
  const res = await fetch(path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': getApiKey(),
    },
    body: body ? JSON.stringify(body) : undefined,
  });

  const data = await res.json().catch(() => null);
  return { ok: res.ok, status: res.status, data: data as T };
}

// Types
export interface Tenant {
  ID: string;
  Name: string;
  Tier: string;
  StripeCustomerID: string;
  BillingEmail: string;
  CreatedAt: string;
  UpdatedAt: string;
}

export interface APIKeyRecord {
  KeyHash: string;
  TenantID: string;
  Name: string;
  Role: string;
  RateLimit: number;
  CreatedAt: string;
  ExpiresAt: string | null;
  Revoked: boolean;
}

export interface InvoiceRecord {
  ID: string;
  TenantID: string;
  PeriodStart: string;
  PeriodEnd: string;
  Tier: string;
  LineItems: Record<string, unknown>;
  Subtotal: number;
  Total: number;
  Currency: string;
  Status: string;
  CreatedAt: string;
  PaidAt: string | null;
}

export interface CreateTenantResponse {
  tenant: Tenant;
  api_key: string;
  warning: string;
}

export interface CreateKeyResponse {
  api_key: string;
  key_hash: string;
  name: string;
  role: string;
  warning: string;
}

// Admin API
export const api = {
  listTenants: () => request<Tenant[]>('GET', '/api/v1/admin/tenants'),
  createTenant: (req: { name: string; tier: string; billing_email: string }) =>
    request<CreateTenantResponse>('POST', '/api/v1/admin/tenants', req),
  getTenant: (id: string) => request<Tenant>('GET', `/api/v1/admin/tenants/${id}`),
  updateTenant: (id: string, req: { name: string; billing_email: string }) =>
    request('PUT', `/api/v1/admin/tenants/${id}`, req),
  createKey: (tenantId: string, req: { name: string; role: string; rate_limit?: number }) =>
    request<CreateKeyResponse>('POST', `/api/v1/admin/tenants/${tenantId}/keys`, req),
  listKeys: (tenantId: string) => request<APIKeyRecord[]>('GET', `/api/v1/admin/tenants/${tenantId}/keys`),
  revokeKey: (hash: string) => request('DELETE', `/api/v1/admin/keys/${hash}`),
  // Billing
  listInvoices: () => request<InvoiceRecord[]>('GET', '/api/v1/billing/invoices'),
};
