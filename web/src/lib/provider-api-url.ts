const RELATIVE_API_ORIGIN = "https://canvas.invalid";

export function appendProviderApiPath(baseUrl: string, path: string, options?: { replacePath?: boolean }) {
    const relative = baseUrl.startsWith("/");
    const base = new URL(baseUrl, RELATIVE_API_ORIGIN);
    const request = new URL(path.startsWith("/") ? path : `/${path}`, RELATIVE_API_ORIGIN);
    const hasPath = Boolean(path.split(/[?#]/u, 1)[0]);
    base.pathname = options?.replacePath ? request.pathname : `${base.pathname.replace(/\/+$/u, "")}${hasPath ? request.pathname : ""}`;
    // Preserve signed query encoding while letting endpoint parameters override matching defaults.
    const requestKeys = new Set(request.searchParams.keys());
    const baseQuery = base.search.slice(1).split("&").filter((entry) => !requestKeys.has(new URLSearchParams(entry).keys().next().value || "")).join("&");
    base.search = [baseQuery, request.search.slice(1)].filter(Boolean).join("&");
    base.hash = "";
    return relative ? `${base.pathname}${base.search}` : base.toString();
}
