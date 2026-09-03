import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createColumnHelper, flexRender, getCoreRowModel, useReactTable } from "@tanstack/react-table";
import {
  Activity, AlertTriangle, ArrowLeft, ArrowRight, Check, ChevronRight, CircleDot,
  Clipboard, Code2, Copy, Database, Eye, EyeOff, FileJson, Gauge, KeyRound, ListFilter,
  Menu, MessageSquare, MoreHorizontal, Network, Plus, Radio, RefreshCw, RotateCw,
  Search, Settings, ShieldAlert, Trash2, Wrench, X,
} from "lucide-react";
import { api, type Project, type RequestDetail, type RequestFilters, type RequestSummary } from "./api";
import { extractMessages, formatBytes, formatDuration, formatTime, modelList, parseEmbeddedJSON, parseJSON, parseSSEEvents, prettyBody, textPreview, type InspectorMessage } from "./lib";
import { Badge, Button, Dialog, Field, Input, Select, Spinner, Tabs, TabsContent, TabsList, TabsTrigger } from "./components/ui";

const statusLabels: Record<string, string> = {
  running: "运行中", completed: "已完成", upstream_error: "上游错误", interrupted: "已中断",
};

const emptyRequests: RequestSummary[] = [];

function StatusBadge({ status }: { status: string }) {
  const tone = status === "completed" ? "green" : status === "running" ? "cyan" : status === "interrupted" ? "amber" : "red";
  return <Badge tone={tone}><span className={status === "running" ? "status-pulse" : "status-dot"} />{statusLabels[status] || status}</Badge>;
}

export default function App() {
  const queryClient = useQueryClient();
  const [selectedProjectId, setSelectedProjectId] = useState("");
  const [selectedRequestId, setSelectedRequestId] = useState("");
  const [filters, setFilters] = useState<RequestFilters>({});
  const [cursor, setCursor] = useState("");
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [toast, setToast] = useState("");

  const projectsQuery = useQuery({ queryKey: ["projects"], queryFn: api.listProjects });
  const projects = projectsQuery.data?.items || [];
  const selectedProject = projects.find((project) => project.id === selectedProjectId);

  useEffect(() => {
    if (!selectedProjectId && projects[0]) setSelectedProjectId(projects[0].id);
    if (selectedProjectId && projects.length && !projects.some((project) => project.id === selectedProjectId)) {
      setSelectedProjectId(projects[0]?.id || "");
    }
  }, [projects, selectedProjectId]);

  useEffect(() => {
    setCursor("");
    setCursorHistory([]);
    setSelectedRequestId("");
  }, [selectedProjectId, filters]);

  const requestsQuery = useQuery({
    queryKey: ["requests", selectedProjectId, filters, cursor],
    queryFn: () => api.listRequests(selectedProjectId, filters, cursor),
    enabled: Boolean(selectedProjectId),
  });

  const detailQuery = useQuery({
    queryKey: ["request", selectedRequestId],
    queryFn: () => api.getRequest(selectedRequestId),
    enabled: Boolean(selectedRequestId),
    refetchInterval: (query) => query.state.data?.status === "running" ? 750 : false,
  });

  useEffect(() => {
    if (!selectedProjectId) return;
    const events = new EventSource(`/api/events?project_id=${encodeURIComponent(selectedProjectId)}`);
    const refresh = (event: MessageEvent) => {
      queryClient.invalidateQueries({ queryKey: ["requests", selectedProjectId] });
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      try {
        const payload = JSON.parse(event.data) as { requestId?: string };
        if (payload.requestId && payload.requestId === selectedRequestId) {
          queryClient.invalidateQueries({ queryKey: ["request", selectedRequestId] });
        }
      } catch { /* keep the live connection resilient */ }
    };
    ["request_started", "request_progress", "request_completed", "requests_cleared", "project_changed"].forEach((name) => events.addEventListener(name, refresh as EventListener));
    return () => events.close();
  }, [queryClient, selectedProjectId, selectedRequestId]);

  useEffect(() => {
    if (!toast) return;
    const timeout = window.setTimeout(() => setToast(""), 2600);
    return () => window.clearTimeout(timeout);
  }, [toast]);

  const columnHelper = createColumnHelper<RequestSummary>();
  const columns = useMemo(() => [
    columnHelper.accessor("status", { header: "状态", cell: (info) => <StatusBadge status={info.getValue()} /> }),
    columnHelper.accessor("path", { header: "接口", cell: (info) => <span className="endpoint"><Code2 size={14} />{info.getValue().replace("/v1/", "")}</span> }),
    columnHelper.accessor("model", { header: "模型", cell: (info) => <span className="model-name">{info.getValue() || "—"}</span> }),
    columnHelper.accessor("streaming", { header: "模式", cell: (info) => info.getValue() ? <Badge tone="purple"><Radio size={12} />stream</Badge> : <span className="muted">JSON</span> }),
    columnHelper.accessor("httpStatus", { header: "HTTP", cell: (info) => <span className={Number(info.getValue()) >= 400 ? "http-error" : "mono"}>{info.getValue() ?? "—"}</span> }),
    columnHelper.accessor("durationMs", { header: "耗时", cell: (info) => <span className="mono">{info.row.original.status === "running" ? "…" : formatDuration(info.getValue())}</span> }),
    columnHelper.accessor("startedAt", { header: "时间", cell: (info) => <span className="time-cell">{formatTime(info.getValue())}</span> }),
    columnHelper.display({ id: "size", header: "响应", cell: (info) => <span className="mono muted">{formatBytes(info.row.original.responseBytes)}</span> }),
    columnHelper.display({ id: "open", header: "", cell: () => <ChevronRight size={16} className="muted" /> }),
  ], []);
  const table = useReactTable({ data: requestsQuery.data?.items ?? emptyRequests, columns, getCoreRowModel: getCoreRowModel() });

  const selectProject = (id: string) => {
    setSelectedProjectId(id);
    setSidebarOpen(false);
  };

  const copy = async (value: string, message = "已复制") => {
    await navigator.clipboard.writeText(value);
    setToast(message);
  };

  return (
    <div className="app-shell">
      <aside className={`sidebar ${sidebarOpen ? "sidebar-open" : ""}`}>
        <div className="brand">
          <div className="brand-mark"><Network size={20} /></div>
          <div><strong>LLM Proxy</strong><span>REQUEST INSPECTOR</span></div>
          <Button variant="ghost" className="sidebar-close" onClick={() => setSidebarOpen(false)}><X size={18} /></Button>
        </div>
        <div className="sidebar-heading"><span>项目</span><Button variant="ghost" aria-label="新建项目" onClick={() => setCreateOpen(true)}><Plus size={17} /></Button></div>
        <nav className="project-list">
          {projects.map((project) => (
            <button key={project.id} className={`project-item ${project.id === selectedProjectId ? "active" : ""}`} onClick={() => selectProject(project.id)}>
              <span className="project-avatar">{project.name.slice(0, 2).toUpperCase()}</span>
              <span className="project-copy"><strong>{project.name}</strong><small>{project.requestCount.toLocaleString()} requests</small></span>
              {project.id === selectedProjectId && <span className="project-active" />}
            </button>
          ))}
          {!projectsQuery.isLoading && projects.length === 0 && <button className="empty-project" onClick={() => setCreateOpen(true)}><Plus size={18} />创建第一个项目</button>}
        </nav>
        <div className="sidebar-foot">
          <div className="security-note"><ShieldAlert size={16} /><span>调试正文以明文保存在本地</span></div>
          <span className="version">LOCAL · v0.1.0</span>
        </div>
      </aside>
      {sidebarOpen && <div className="sidebar-scrim" onClick={() => setSidebarOpen(false)} />}

      <main className="main-panel">
        <header className="topbar">
          <div className="topbar-title">
            <Button variant="ghost" className="mobile-menu" onClick={() => setSidebarOpen(true)}><Menu size={19} /></Button>
            <div><div className="eyebrow"><CircleDot size={12} /> LIVE CAPTURE</div><h1>{selectedProject?.name || "LLM 请求调试台"}</h1></div>
          </div>
          <div className="topbar-actions">
            {selectedProject && <>
              <Button variant="outline" onClick={() => copy(`${location.origin}/v1`, "BaseURL 已复制")}><Copy size={15} />复制 BaseURL</Button>
              <Button variant="ghost" aria-label="项目设置" onClick={() => setSettingsOpen(true)}><Settings size={18} /></Button>
            </>}
          </div>
        </header>

        {location.hostname !== "127.0.0.1" && location.hostname !== "localhost" && (
          <div className="public-warning"><AlertTriangle size={16} /><span>当前通过非本机地址访问。管理台没有登录保护，请确认网络边界可信。</span></div>
        )}

        {!selectedProject ? (
          <EmptyWorkspace onCreate={() => setCreateOpen(true)} loading={projectsQuery.isLoading} />
        ) : (
          <section className="workspace">
            <div className="summary-strip">
              <Metric icon={<Activity size={16} />} label="当前页请求" value={String(requestsQuery.data?.items.length || 0)} />
              <Metric icon={<Gauge size={16} />} label="运行中" value={String(requestsQuery.data?.items.filter((item) => item.status === "running").length || 0)} accent />
              <Metric icon={<Database size={16} />} label="项目记录" value={selectedProject.requestCount.toLocaleString()} />
              <div className="connection-chip"><span className="live-dot" />实时连接</div>
            </div>

            <div className="toolbar">
              <div className="filter-icon"><ListFilter size={17} /><span>筛选</span></div>
              <Select aria-label="状态筛选" value={filters.status || ""} onChange={(event) => setFilters((value) => ({ ...value, status: event.target.value }))}>
                <option value="">全部状态</option><option value="running">运行中</option><option value="completed">已完成</option><option value="upstream_error">上游错误</option><option value="interrupted">已中断</option>
              </Select>
              <Select aria-label="接口筛选" value={filters.path || ""} onChange={(event) => setFilters((value) => ({ ...value, path: event.target.value }))}>
                <option value="">全部接口</option><option value="/v1/chat/completions">chat/completions</option><option value="/v1/models">models</option>
              </Select>
              <Select aria-label="流式筛选" value={filters.streaming || ""} onChange={(event) => setFilters((value) => ({ ...value, streaming: event.target.value }))}>
                <option value="">全部模式</option><option value="true">Stream</option><option value="false">JSON</option>
              </Select>
              <div className="search-field"><Search size={15} /><Input aria-label="模型筛选" placeholder="筛选模型…" value={filters.model || ""} onChange={(event) => setFilters((value) => ({ ...value, model: event.target.value }))} /></div>
              <Button variant="ghost" aria-label="刷新请求" onClick={() => requestsQuery.refetch()}><RefreshCw size={16} className={requestsQuery.isFetching ? "spin" : ""} /></Button>
            </div>

            <div className="table-shell">
              <table className="request-table">
                <thead>{table.getHeaderGroups().map((group) => <tr key={group.id}>{group.headers.map((header) => <th key={header.id}>{flexRender(header.column.columnDef.header, header.getContext())}</th>)}</tr>)}</thead>
                <tbody>
                  {table.getRowModel().rows.map((row) => (
                    <tr key={row.id} className={selectedRequestId === row.original.id ? "selected" : ""} onClick={() => setSelectedRequestId(row.original.id)}>
                      {row.getVisibleCells().map((cell) => <td key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</td>)}
                    </tr>
                  ))}
                </tbody>
              </table>
              {requestsQuery.isLoading && <div className="table-state"><Spinner />加载请求…</div>}
              {!requestsQuery.isLoading && table.getRowModel().rows.length === 0 && <div className="table-state empty"><Network size={28} /><strong>还没有匹配的请求</strong><span>把 OpenAI 客户端 BaseURL 指向 <code>{location.origin}/v1</code></span></div>}
            </div>
            <div className="pagination">
              <span>每页最多 50 条</span>
              <div>
                <Button variant="ghost" disabled={!cursorHistory.length} onClick={() => { const history = [...cursorHistory]; setCursor(history.pop() || ""); setCursorHistory(history); }}><ArrowLeft size={15} />上一页</Button>
                <Button variant="ghost" disabled={!requestsQuery.data?.nextCursor} onClick={() => { setCursorHistory((value) => [...value, cursor]); setCursor(requestsQuery.data?.nextCursor || ""); }}>下一页<ArrowRight size={15} /></Button>
              </div>
            </div>
          </section>
        )}
      </main>

      {selectedRequestId && <RequestInspector detail={detailQuery.data} loading={detailQuery.isLoading} error={detailQuery.error?.message} onClose={() => setSelectedRequestId("")} onCopy={copy} />}
      <CreateProjectDialog open={createOpen} onOpenChange={setCreateOpen} onCreated={(project) => { setSelectedProjectId(project.id); queryClient.invalidateQueries({ queryKey: ["projects"] }); }} />
      {selectedProject && <ProjectSettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} project={selectedProject} onChanged={() => queryClient.invalidateQueries({ queryKey: ["projects"] })} onDeleted={() => { setSettingsOpen(false); setSelectedProjectId(""); queryClient.invalidateQueries({ queryKey: ["projects"] }); }} onToast={setToast} />}
      {toast && <div className="toast"><Check size={16} />{toast}</div>}
    </div>
  );
}

function Metric({ icon, label, value, accent = false }: { icon: React.ReactNode; label: string; value: string; accent?: boolean }) {
  return <div className={`metric ${accent ? "metric-accent" : ""}`}><span>{icon}{label}</span><strong>{value}</strong></div>;
}

function EmptyWorkspace({ onCreate, loading }: { onCreate: () => void; loading: boolean }) {
  return <div className="empty-workspace">{loading ? <Spinner /> : <><div className="empty-orbit"><Network size={34} /></div><span className="eyebrow">OPENAI-COMPATIBLE CAPTURE</span><h2>看清 Agent 的每一次模型交互</h2><p>创建项目后会得到独立 API Key。所有 Chat Completions 和 Models 请求都会在这里实时出现。</p><Button onClick={onCreate}><Plus size={16} />创建项目</Button></>}</div>;
}

function CreateProjectDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (value: boolean) => void; onCreated: (project: Project) => void }) {
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("https://api.openai.com/v1");
  const [upstreamApiKey, setUpstreamApiKey] = useState("");
  const [createdKey, setCreatedKey] = useState("");
  const mutation = useMutation({ mutationFn: api.createProject, onSuccess: (result) => { setCreatedKey(result.apiKey); onCreated(result.project); } });
  const close = (value: boolean) => { onOpenChange(value); if (!value) { setCreatedKey(""); setName(""); mutation.reset(); } };
  return <Dialog open={open} onOpenChange={close} title={createdKey ? "项目已创建" : "创建调试项目"} description={createdKey ? "客户端使用下面的 API Key 访问代理。之后仍可在设置中查看。" : "一个项目对应一个上游与一个独立的代理 API Key。"} footer={createdKey ? <Button onClick={() => close(false)}>完成</Button> : <Button disabled={!name.trim() || !baseUrl.trim() || mutation.isPending} onClick={() => mutation.mutate({ name, baseUrl, upstreamApiKey })}>{mutation.isPending ? <Spinner /> : <Plus size={15} />}创建项目</Button>}>
    {createdKey ? <SecretReveal value={createdKey} /> : <div className="form-stack">
      <Field label="项目名称"><Input autoFocus value={name} onChange={(event) => setName(event.target.value)} placeholder="Agentic RAG - Dev" /></Field>
      <Field label="上游 BaseURL" hint="填写 SDK 使用的 API 根地址，服务会追加 /chat/completions 或 /models。"><Input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="https://api.openai.com/v1" /></Field>
      <Field label="上游 API Key" hint="可留空，适用于本地 OpenAI-compatible 服务。"><Input type="password" autoComplete="off" value={upstreamApiKey} onChange={(event) => setUpstreamApiKey(event.target.value)} placeholder="sk-…" /></Field>
      {mutation.error && <div className="form-error"><AlertTriangle size={15} />{mutation.error.message}</div>}
    </div>}
  </Dialog>;
}

function ProjectSettingsDialog({ open, onOpenChange, project, onChanged, onDeleted, onToast }: { open: boolean; onOpenChange: (value: boolean) => void; project: Project; onChanged: () => void; onDeleted: () => void; onToast: (message: string) => void }) {
  const [name, setName] = useState(project.name);
  const [baseUrl, setBaseUrl] = useState(project.baseUrl);
  const [newUpstreamKey, setNewUpstreamKey] = useState("");
  const [revealedKey, setRevealedKey] = useState("");
  const [deleteConfirm, setDeleteConfirm] = useState("");
  const [testResult, setTestResult] = useState("");
  useEffect(() => { if (open) { setName(project.name); setBaseUrl(project.baseUrl); setNewUpstreamKey(""); setRevealedKey(""); setDeleteConfirm(""); setTestResult(""); } }, [open, project]);
  const update = useMutation({ mutationFn: () => api.updateProject(project.id, { name, baseUrl, ...(newUpstreamKey ? { upstreamApiKey: newUpstreamKey } : {}) }), onSuccess: () => { onChanged(); onOpenChange(false); onToast("项目设置已保存"); } });
  const clearUpstream = useMutation({ mutationFn: () => api.updateProject(project.id, { name, baseUrl, upstreamApiKey: "" }), onSuccess: () => { setNewUpstreamKey(""); onChanged(); onToast("上游 API Key 已清除"); } });
  const reveal = useMutation({ mutationFn: () => api.revealKey(project.id), onSuccess: (result) => setRevealedKey(result.apiKey) });
  const rotate = useMutation({ mutationFn: () => api.rotateKey(project.id), onSuccess: (result) => { setRevealedKey(result.apiKey); onChanged(); onToast("旧 API Key 已立即失效"); } });
  const test = useMutation({ mutationFn: () => api.testUpstream(project.id), onSuccess: (result) => setTestResult(result.ok ? `连接成功 · HTTP ${result.status} · ${formatDuration(result.durationMs)}` : `连接失败 · HTTP ${result.status}`), onError: (error) => setTestResult(error.message) });
  const clear = useMutation({ mutationFn: () => api.clearRequests(project.id), onSuccess: (result) => { onChanged(); onToast(`已清除 ${result.deleted} 条记录`); } });
  const remove = useMutation({ mutationFn: () => api.deleteProject(project.id), onSuccess: onDeleted });
  return <Dialog open={open} onOpenChange={onOpenChange} title="项目设置" description="修改上游连接、管理项目 API Key 和本地抓包记录。" footer={<><Button variant="outline" onClick={() => onOpenChange(false)}>取消</Button><Button disabled={update.isPending} onClick={() => update.mutate()}>{update.isPending ? <Spinner /> : <Check size={15} />}保存设置</Button></>}>
    <Tabs defaultValue="connection" className="settings-tabs">
      <TabsList className="tabs-list"><TabsTrigger value="connection">连接</TabsTrigger><TabsTrigger value="credentials">凭据</TabsTrigger><TabsTrigger value="danger">数据与删除</TabsTrigger></TabsList>
      <TabsContent value="connection" className="tab-content form-stack">
        <Field label="项目名称"><Input value={name} onChange={(event) => setName(event.target.value)} /></Field>
        <Field label="上游 BaseURL"><Input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} /></Field>
        <Field label="替换上游 API Key" hint={`当前：${project.upstreamApiKeyMasked || "未配置"}。留空表示不修改。`}><Input type="password" value={newUpstreamKey} onChange={(event) => setNewUpstreamKey(event.target.value)} placeholder="输入新的 Key" /></Field>
        {project.upstreamApiKeyMasked && <Button variant="ghost" onClick={() => { if (confirm("确认清除上游 API Key？之后代理将不再发送 Authorization。")) clearUpstream.mutate(); }}><Trash2 size={14} />清除上游 Key</Button>}
        <div className="inline-action"><Button variant="outline" onClick={() => test.mutate()} disabled={test.isPending}>{test.isPending ? <Spinner /> : <Activity size={15} />}测试 /models</Button>{testResult && <span>{testResult}</span>}</div>
      </TabsContent>
      <TabsContent value="credentials" className="tab-content form-stack">
        <div className="credential-summary"><KeyRound size={20} /><div><strong>项目 API Key</strong><span>{project.apiKeyPrefix}••••••••</span></div></div>
        {revealedKey ? <SecretReveal value={revealedKey} /> : <Button variant="outline" onClick={() => reveal.mutate()} disabled={reveal.isPending}><Eye size={15} />查看完整 Key</Button>}
        <div className="warning-box"><AlertTriangle size={17} /><span>轮换后旧 Key 会立即失效，正在运行的请求不受影响。</span></div>
        <Button variant="outline" onClick={() => { if (confirm("确认轮换项目 API Key？旧 Key 会立即失效。")) rotate.mutate(); }}><RotateCw size={15} />轮换 API Key</Button>
      </TabsContent>
      <TabsContent value="danger" className="tab-content form-stack">
        <div className="danger-section"><div><strong>清空抓包记录</strong><span>运行中的请求不会删除。</span></div><Button variant="outline" onClick={() => { if (confirm("确认清空该项目已完成的全部请求记录？")) clear.mutate(); }}><Trash2 size={15} />清空记录</Button></div>
        <div className="danger-section delete"><div><strong>删除项目</strong><span>项目 Key 与全部记录会永久删除。请输入项目名确认。</span></div><Input value={deleteConfirm} onChange={(event) => setDeleteConfirm(event.target.value)} placeholder={project.name} /><Button variant="danger" disabled={deleteConfirm !== project.name || remove.isPending} onClick={() => remove.mutate()}><Trash2 size={15} />永久删除</Button></div>
      </TabsContent>
    </Tabs>
  </Dialog>;
}

function SecretReveal({ value }: { value: string }) {
  const [visible, setVisible] = useState(true);
  return <div className="secret-reveal"><code>{visible ? value : "•".repeat(Math.min(48, value.length))}</code><Button variant="ghost" aria-label={visible ? "隐藏" : "显示"} onClick={() => setVisible((value) => !value)}>{visible ? <EyeOff size={16} /> : <Eye size={16} />}</Button><Button variant="ghost" aria-label="复制" onClick={() => navigator.clipboard.writeText(value)}><Copy size={16} /></Button></div>;
}

function RequestInspector({ detail, loading, error, onClose, onCopy }: { detail?: RequestDetail; loading: boolean; error?: string; onClose: () => void; onCopy: (value: string, message?: string) => void }) {
  const [rawSide, setRawSide] = useState<"request" | "response">("request");
  const [wrap, setWrap] = useState(true);
  return <aside className="inspector">
    <div className="inspector-header"><div><span className="eyebrow">REQUEST DETAIL</span><strong>{detail?.id || "加载中…"}</strong></div><Button variant="ghost" aria-label="关闭详情" onClick={onClose}><X size={18} /></Button></div>
    {loading && <div className="inspector-state"><Spinner />读取抓包…</div>}
    {error && <div className="inspector-state error"><AlertTriangle />{error}</div>}
    {detail && <>
      <div className="inspector-summary"><StatusBadge status={detail.status} /><span className="endpoint">{detail.method} {detail.path}</span><span className="mono">{detail.model || "—"}</span>{(detail.requestTruncated || detail.responseTruncated) && <Badge tone="amber">TRUNCATED</Badge>}</div>
      {detail.error && <div className="detail-error"><AlertTriangle size={15} />{detail.error}</div>}
      <Tabs defaultValue="messages" className="detail-tabs">
        <TabsList className="tabs-list inspector-tabs"><TabsTrigger value="messages"><MessageSquare size={14} />消息</TabsTrigger><TabsTrigger value="raw"><FileJson size={14} />原始包</TabsTrigger><TabsTrigger value="sse"><Radio size={14} />SSE</TabsTrigger><TabsTrigger value="headers"><Network size={14} />Headers</TabsTrigger><TabsTrigger value="meta"><MoreHorizontal size={14} />元数据</TabsTrigger></TabsList>
        <TabsContent value="messages" className="tab-content inspector-content"><MessagesView detail={detail} onCopy={onCopy} /></TabsContent>
        <TabsContent value="raw" className="tab-content inspector-content">
          <div className="code-toolbar"><div className="segmented"><button className={rawSide === "request" ? "active" : ""} onClick={() => setRawSide("request")}>Request</button><button className={rawSide === "response" ? "active" : ""} onClick={() => setRawSide("response")}>Response</button></div><div><Button variant="ghost" onClick={() => setWrap((value) => !value)}><Clipboard size={14} />{wrap ? "不换行" : "自动换行"}</Button><Button variant="ghost" onClick={() => onCopy(rawSide === "request" ? detail.requestBody : detail.responseBody)}><Copy size={14} />复制</Button></div></div>
          <pre className={`code-block ${wrap ? "wrap" : ""}`}>{prettyBody(rawSide === "request" ? detail.requestBody : detail.responseBody) || "（空响应体）"}</pre>
        </TabsContent>
        <TabsContent value="sse" className="tab-content inspector-content"><SSEView detail={detail} /></TabsContent>
        <TabsContent value="headers" className="tab-content inspector-content"><HeadersView detail={detail} /></TabsContent>
        <TabsContent value="meta" className="tab-content inspector-content"><MetadataView detail={detail} /></TabsContent>
      </Tabs>
    </>}
  </aside>;
}

function MessagesView({ detail, onCopy }: { detail: RequestDetail; onCopy: (value: string, message?: string) => void }) {
  if (detail.path === "/v1/models") {
    const models = modelList(detail.responseBody);
    return <div className="models-view">{models.length ? models.map((model) => <div className="model-card" key={model.id}><div><Database size={16} /><strong>{model.id}</strong></div><span>{model.owned_by || "unknown owner"}</span></div>) : <div className="inline-empty">响应中没有可识别的模型列表，请查看原始包。</div>}</div>;
  }
  const messages = extractMessages(detail);
  return <div className="message-flow">
    <div className="flow-label"><span>REQUEST MESSAGES</span><b>{messages.request.length}</b></div>
    {messages.request.map((message, index) => <MessageCard key={`request-${index}`} message={message} index={index} onCopy={onCopy} />)}
    <div className="flow-divider"><span>UPSTREAM RESPONSE</span></div>
    {messages.response.map((message, index) => <MessageCard key={`response-${index}`} message={message} index={index} response onCopy={onCopy} />)}
    {!messages.response.length && <div className="inline-empty">{detail.status === "running" ? "正在等待完整消息…" : "没有可识别的 message，请查看原始响应。"}</div>}
  </div>;
}

function MessageCard({ message, index, response = false, onCopy }: { message: InspectorMessage; index: number; response?: boolean; onCopy: (value: string, message?: string) => void }) {
  const role = message.role || "unknown";
  const content = typeof message.content === "string" ? message.content : message.content == null ? "" : JSON.stringify(message.content, null, 2);
  const parsedContent = parseEmbeddedJSON(message.content);
  const structuredToolResult = (role === "tool" || role === "function") && parsedContent.isJSON;
  return <article className={`message-card role-${role}`}>
    <header><span className="message-index">{response ? "R" : index + 1}</span><Badge tone={role === "assistant" ? "cyan" : role === "tool" || role === "function" ? "purple" : role === "system" || role === "developer" ? "amber" : "neutral"}>{role}</Badge>{message.name && <span className="muted">{message.name}</span>}{message.tool_call_id && <code>{message.tool_call_id}</code>}</header>
    {content && !structuredToolResult && <pre className="message-content">{content}</pre>}
    {structuredToolResult && <ToolResultCard value={parsedContent.value} raw={content} onCopy={onCopy} />}
    {message.refusal != null && <StructuredField icon={<ShieldAlert size={14} />} label="refusal" value={message.refusal} />}
    {message.function_call != null && <ToolCallCard label="function_call" value={message.function_call} onCopy={onCopy} />}
    {Array.isArray(message.tool_calls) && message.tool_calls.map((tool, toolIndex) => <ToolCallCard key={toolIndex} label={`tool_call #${toolIndex}`} value={tool} onCopy={onCopy} />)}
  </article>;
}

type UnknownRecord = Record<string, unknown>;

function asRecord(value: unknown): UnknownRecord | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as UnknownRecord : null;
}

function displayValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value == null || typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value, null, 2);
}

function inlineValue(value: unknown): string {
  const displayed = displayValue(value).replace(/\s+/g, " ");
  return displayed.length > 72 ? `${displayed.slice(0, 72).trimEnd()}…` : displayed;
}

function shortCallID(value: string): string {
  return value.length > 20 ? `${value.slice(0, 11)}…${value.slice(-5)}` : value;
}

function byteLength(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

function ToolCallCard({ label, value, onCopy }: { label: string; value: unknown; onCopy: (value: string, message?: string) => void }) {
  const tool = asRecord(value);
  const fn = asRecord(tool?.function) || tool;
  const name = typeof fn?.name === "string" ? fn.name : "unknown tool";
  const callID = typeof tool?.id === "string" ? tool.id : "";
  const argumentSource = fn?.arguments;
  const parsedArguments = parseEmbeddedJSON(argumentSource);
  const argumentRecord = asRecord(parsedArguments.value);
  const argumentEntries = argumentRecord ? Object.entries(argumentRecord) : [];
  const preview = argumentEntries.slice(0, 3).map(([key, entryValue]) => `${key}=${inlineValue(entryValue)}`).join(" · ");
  const raw = JSON.stringify(value, null, 2) || String(value);

  return <details className="tool-call-card">
    <summary>
      <ChevronRight size={14} className="details-chevron" />
      <span className="tool-kind"><Wrench size={13} />{label}</span>
      <strong>{name}</strong>
      {preview && <span className="tool-preview">{preview}</span>}
      {callID && <code title={callID}>{shortCallID(callID)}</code>}
    </summary>
    <div className="tool-card-body">
      <div className="tool-section-label">调用参数</div>
      {argumentEntries.length > 0 ? <dl className="tool-arguments">
        {argumentEntries.map(([key, entryValue]) => <div key={key}><dt>{key}</dt><dd>{displayValue(entryValue)}</dd></div>)}
      </dl> : <pre className="tool-code">{displayValue(parsedArguments.value ?? argumentSource ?? "无参数")}</pre>}
      <RawJSON value={raw} onCopy={onCopy} />
    </div>
  </details>;
}

function ToolResultCard({ value, raw, onCopy }: { value: unknown; raw: string; onCopy: (value: string, message?: string) => void }) {
  const result = asRecord(value);
  const data = Array.isArray(result?.data) ? result.data : Array.isArray(value) ? value : null;
  const success = typeof result?.success === "boolean" ? result.success : null;
  const formatted = JSON.stringify(value, null, 2) || raw;
  const visibleResults = data?.slice(0, 50) || [];

  return <details className="tool-result-card">
    <summary>
      <ChevronRight size={14} className="details-chevron" />
      <strong>工具返回</strong>
      {success !== null && <Badge tone={success ? "green" : "red"}>{success ? "成功" : "失败"}</Badge>}
      {data && <span>{data.length} 条结果</span>}
      <code>{formatBytes(byteLength(raw || formatted))}</code>
    </summary>
    <div className="tool-card-body">
      {data ? <>
        <div className="tool-section-label">结果列表 <span>{data.length}</span></div>
        <div className="tool-result-list">
          {visibleResults.map((item, resultIndex) => <ToolResultItem key={resultIndex} value={item} index={resultIndex} />)}
        </div>
        {data.length > visibleResults.length && <div className="tool-result-note">仅展示前 {visibleResults.length} 条；完整内容可在原始 JSON 中查看。</div>}
      </> : <pre className="tool-code tool-result-json">{formatted}</pre>}
      <RawJSON value={formatted} onCopy={onCopy} />
    </div>
  </details>;
}

function ToolResultItem({ value, index }: { value: unknown; index: number }) {
  const item = asRecord(value);
  const uri = typeof item?.uri === "string" ? item.uri : "";
  const name = [item?.title, item?.name, uri && uri.split(/[\\/]/).pop(), item?.document_id]
    .find((candidate): candidate is string => typeof candidate === "string" && Boolean(candidate)) || `结果 #${index + 1}`;
  const contentEntry = ["chunk_content", "content", "text"].find((key) => typeof item?.[key] === "string");
  const content = contentEntry ? String(item?.[contentEntry]) : "";
  const chunkID = item?.chunk_id;
  const score = typeof item?.combined_rank === "number" ? item.combined_rank : typeof item?.score === "number" ? item.score : null;
  const metadata = item ? Object.entries(item).filter(([key]) => key !== contentEntry).slice(0, 8) : [];
  const fallback = JSON.stringify(value, null, 2) || String(value);

  return <details className="tool-result-item">
    <summary>
      <ChevronRight size={13} className="details-chevron" />
      <span className="result-index">{index + 1}</span>
      <span className="result-summary">
        <strong title={uri || name}>{name}</strong>
        {content && <span>{textPreview(content)}</span>}
      </span>
      <span className="result-meta">{chunkID != null && `chunk ${String(chunkID)}`}{score != null && ` · ${score.toFixed(4)}`}</span>
    </summary>
    <div className="tool-result-detail">
      {metadata.length > 0 && <dl>
        {metadata.map(([key, entryValue]) => <div key={key}><dt>{key}</dt><dd>{displayValue(entryValue)}</dd></div>)}
      </dl>}
      <pre>{content || fallback}</pre>
    </div>
  </details>;
}

function RawJSON({ value, onCopy }: { value: string; onCopy: (value: string, message?: string) => void }) {
  return <details className="tool-raw">
    <summary><FileJson size={13} />原始 JSON</summary>
    <div className="tool-raw-toolbar"><Button variant="ghost" onClick={() => onCopy(value, "JSON 已复制")}><Copy size={13} />复制 JSON</Button></div>
    <pre>{value}</pre>
  </details>;
}

function StructuredField({ icon, label, value }: { icon: React.ReactNode; label: string; value: unknown }) {
  return <div className="structured-field"><strong>{icon}{label}</strong><pre>{JSON.stringify(value, null, 2)}</pre></div>;
}

function SSEView({ detail }: { detail: RequestDetail }) {
  if (!detail.streaming) return <div className="inline-empty"><Radio size={22} />该请求不是流式请求。</div>;
  const events = parseSSEEvents(detail.responseBody);
  return <div className="sse-list"><div className="flow-label"><span>SERVER-SENT EVENTS</span><b>{events.length}</b></div>{events.map((event) => <details className="sse-event" key={event.index} open={event.index < 2}><summary><span>#{event.index + 1}</span><Badge tone={event.data === "[DONE]" ? "green" : "purple"}>{event.data === "[DONE]" ? "DONE" : event.event}</Badge><code>{event.parsed && typeof event.parsed === "object" ? String((event.parsed as { object?: string }).object || "JSON chunk") : "raw"}</code></summary><pre>{event.parsed ? JSON.stringify(event.parsed, null, 2) : event.data}</pre></details>)}</div>;
}

function HeadersView({ detail }: { detail: RequestDetail }) {
  return <div className="headers-grid"><HeaderTable title="Client Request" values={detail.requestHeaders} /><HeaderTable title="Upstream Response" values={detail.responseHeaders} /></div>;
}

function HeaderTable({ title, values }: { title: string; values: Record<string, string[]> }) {
  return <section className="header-section"><div className="flow-label"><span>{title}</span><b>{Object.keys(values).length}</b></div><dl>{Object.entries(values).sort(([a], [b]) => a.localeCompare(b)).map(([key, list]) => <div key={key}><dt>{key}</dt><dd>{list.join("\n")}</dd></div>)}</dl></section>;
}

function MetadataView({ detail }: { detail: RequestDetail }) {
  const rows = [
    ["Request ID", detail.id], ["Project ID", detail.projectId], ["Upstream URL", detail.upstreamUrl],
    ["Started", new Date(detail.startedAt).toLocaleString("zh-CN")], ["Finished", detail.finishedAt ? new Date(detail.finishedAt).toLocaleString("zh-CN") : "—"],
    ["Duration", formatDuration(detail.durationMs)], ["HTTP Status", detail.httpStatus ?? "—"], ["Streaming", detail.streaming ? "true" : "false"],
    ["Request bytes", `${detail.requestBytes} (${formatBytes(detail.requestBytes)})`], ["Response bytes", `${detail.responseBytes} (${formatBytes(detail.responseBytes)})`],
    ["Request truncated", String(detail.requestTruncated)], ["Response truncated", String(detail.responseTruncated)],
  ];
  return <dl className="metadata-list">{rows.map(([key, value]) => <div key={String(key)}><dt>{key}</dt><dd>{String(value)}</dd></div>)}</dl>;
}
