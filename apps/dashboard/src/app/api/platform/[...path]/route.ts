import { NextResponse } from "next/server";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const HOP_HEADERS = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "host",
  "content-length",
  "content-encoding",
]);

function upstreamBase(): string {
  return (
    process.env.DASHBOARD_API_URL ??
    process.env.API_URL ??
    process.env.NEXT_PUBLIC_API_URL ??
    "http://localhost:8080"
  ).replace(/\/$/, "");
}

async function proxy(request: Request, context: { params: Promise<{ path: string[] }> }) {
  const { path } = await context.params;
  const incoming = new URL(request.url);
  const target = new URL(`${upstreamBase()}/api/v1/${path.join("/")}`);
  target.search = incoming.search;

  const headers = new Headers();
  for (const [key, value] of request.headers) {
    if (!HOP_HEADERS.has(key.toLowerCase()) && !key.toLowerCase().startsWith("x-dashboard-")) {
      headers.set(key, value);
    }
  }
  const clientKey = request.headers.get("x-dashboard-api-key")?.trim();
  const serverKey = process.env.DASHBOARD_API_KEY?.trim();
  const serverToken = process.env.DASHBOARD_TOKEN?.trim();
  if (serverKey) {
    headers.set("x-api-key", serverKey);
  } else if (clientKey) {
    headers.set("x-api-key", clientKey);
  } else if (serverToken) {
    headers.set("authorization", `Bearer ${serverToken}`);
  }

  const init: RequestInit = {
    method: request.method,
    headers,
    redirect: "manual",
    cache: "no-store",
  };
  if (request.method !== "GET" && request.method !== "HEAD") {
    init.body = await request.arrayBuffer();
  }

  let response: Response;
  try {
    response = await fetch(target, init);
  } catch {
    return NextResponse.json(
      {
        error: {
          code: "dashboard_upstream_unavailable",
          message: "The platform API is unreachable.",
        },
      },
      { status: 502 },
    );
  }

  const responseHeaders = new Headers();
  for (const [key, value] of response.headers) {
    if (!HOP_HEADERS.has(key.toLowerCase())) responseHeaders.set(key, value);
  }
  responseHeaders.set("cache-control", "no-store");
  if (response.headers.get("content-type")?.includes("text/event-stream")) {
    responseHeaders.set("x-accel-buffering", "no");
  }
  return new Response(response.body, {
    status: response.status,
    headers: responseHeaders,
  });
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const PATCH = proxy;
export const DELETE = proxy;
