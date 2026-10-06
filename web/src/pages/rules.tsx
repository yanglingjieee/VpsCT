import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Star, Trash2 } from "lucide-react";
import { del, get, post, put } from "@/lib/api";
import type { RuleKind, Ruleset, RulesetList } from "@/lib/types";
import { cn, fmtAgo } from "@/lib/utils";
import { Badge, Button, Card, Code, Confirm, Dialog, Field, Input, PageHeader, Spinner, Tabs, Textarea } from "@/components/ui";
import { useToast } from "@/components/toast";
import { RULE_KIND_LABELS } from "@/lib/clients";

const KINDS: RuleKind[] = ["mihomo", "shadowrocket", "surge", "singbox"];

export function RulesPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["rulesets"], queryFn: () => get<RulesetList>("/api/v1/rulesets") });
  const [dialog, setDialog] = React.useState<{ rule?: Ruleset } | null>(null);
  const [view, setView] = React.useState(false);
  const [confirm, setConfirm] = React.useState<Ruleset | null>(null);
  const invalidate = () => { qc.invalidateQueries({ queryKey: ["rulesets"] }); qc.invalidateQueries({ queryKey: ["shares"] }); };
  const setDefault = useMutation({ mutationFn: (id: number) => post(`/api/v1/rulesets/${id}/default`), onSuccess: () => { toast.success("已设为新用户的默认规则"); invalidate(); }, onError: (e) => toast.fromError(e) });
  const remove = useMutation({ mutationFn: (r: Ruleset) => del(`/api/v1/rulesets/${r.id}`), onSuccess: () => { toast.success("规则已删除"); setConfirm(null); invalidate(); }, onError: (e) => toast.fromError(e) });
  if (q.isLoading || !q.data) return <Spinner />;
  const d = q.data;
  return (
    <div>
      <PageHeader title="规则" description="一套规则给每种客户端各写一份，用户一个链接到处都能用" actions={<Button onClick={() => setDialog({})}><Plus className="h-4 w-4" /> 新建规则</Button>} />
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {d.list.map((r) => (
          <Card key={r.id} className="flex h-full flex-col p-4">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <p className="break-words font-medium">{r.name}</p>
                <p className="break-words text-xs text-muted-foreground">{r.description || "—"}</p>
              </div>
              {r.default && <Badge variant="success">默认</Badge>}
            </div>
            <div className="mt-3 flex flex-wrap gap-1.5">{r.formats.map((k) => <Badge key={k} variant="outline">{RULE_KIND_LABELS[k]}</Badge>)}</div>
            <p className="mt-3 text-xs text-muted-foreground">{r.users} 个用户在用 · {fmtAgo(r.updated_at)}修改</p>
            <div className="mt-auto flex justify-end gap-1 pt-3">
              {!r.default && <Button size="sm" variant="ghost" onClick={() => setDefault.mutate(r.id)}><Star className="h-4 w-4" /> 设为默认</Button>}
              <Button size="sm" variant="ghost" aria-label="编辑" onClick={() => setDialog({ rule: r })}><Pencil className="h-4 w-4" /></Button>
              <Button size="sm" variant="ghost" aria-label="删除" className="text-red-500" onClick={() => setConfirm(r)}><Trash2 className="h-4 w-4" /></Button>
            </div>
          </Card>
        ))}
        <Card className="flex h-full flex-col border-dashed p-4">
          <div className="flex items-start justify-between gap-2">
            <div className="min-w-0">
              <p className="font-medium">无规则</p>
              <p className="text-xs text-muted-foreground">内置：除了局域网，全部走用户选的线路</p>
            </div>
            {d.default_id === 0 && <Badge variant="success">默认</Badge>}
          </div>
          <div className="mt-3 flex flex-wrap gap-1.5">{KINDS.map((k) => <Badge key={k} variant="outline">{RULE_KIND_LABELS[k]}</Badge>)}</div>
          <p className="mt-3 text-xs text-muted-foreground">{d.none_users} 个用户在用</p>
          <div className="mt-auto flex justify-end gap-1 pt-3">
            {d.default_id !== 0 && <Button size="sm" variant="ghost" onClick={() => setDefault.mutate(0)}><Star className="h-4 w-4" /> 设为默认</Button>}
            <Button size="sm" variant="ghost" onClick={() => setView(true)}>查看</Button>
          </div>
        </Card>
      </div>
      <RuleDialog state={dialog} none={d.none} onClose={() => setDialog(null)} onSaved={invalidate} />
      <RuleDialog state={view ? {} : null} none={d.none} readOnly onClose={() => setView(false)} onSaved={invalidate} />
      <Confirm open={!!confirm} onClose={() => setConfirm(null)} onConfirm={() => confirm && remove.mutate(confirm)} loading={remove.isPending} destructive title={`删除规则「${confirm?.name ?? ""}」？`} description={`${confirm?.users ?? 0} 个用户会在下次更新配置时变成「无规则」。`} />
    </div>
  );
}

type Draft = { name: string; description: string } & Record<RuleKind, string>;

function RuleDialog({ state, none, readOnly, onClose, onSaved }: { state: { rule?: Ruleset } | null; none: Record<RuleKind, string>; readOnly?: boolean; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const rule = state?.rule;
  const [f, setF] = React.useState<Draft>({ name: "", description: "", mihomo: "", shadowrocket: "", surge: "", singbox: "" });
  const [kind, setKind] = React.useState<RuleKind>("mihomo");
  React.useEffect(() => {
    if (!state) return;
    setKind("mihomo");
    if (readOnly) setF({ name: "无规则", description: "", ...none });
    else setF(rule ? { name: rule.name, description: rule.description, mihomo: rule.mihomo, shadowrocket: rule.shadowrocket, surge: rule.surge, singbox: rule.singbox } : { name: "", description: "", mihomo: "", shadowrocket: "", surge: "", singbox: "" });
  }, [state, rule, readOnly, none]);
  const save = useMutation({
    mutationFn: () => (rule ? put(`/api/v1/rulesets/${rule.id}`, { ...f, sort_order: rule.sort_order }) : post("/api/v1/rulesets", f)),
    onSuccess: () => { toast.success("规则已保存，用户下次更新配置时生效"); onSaved(); onClose(); },
    onError: (e) => toast.fromError(e),
  });
  return (
    <Dialog open={!!state} onClose={onClose} title={readOnly ? "无规则（内置）" : rule ? "编辑规则" : "新建规则"} size="lg"
      footer={readOnly ? <Button variant="outline" onClick={onClose}>关闭</Button> : <><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={() => save.mutate()} loading={save.isPending} disabled={!f.name.trim()}>保存</Button></>}>
      <div className="space-y-4">
        {!readOnly && (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="名称"><Input value={f.name} onChange={(e) => setF((p) => ({ ...p, name: e.target.value }))} placeholder="白名单分流" /></Field>
            <Field label="说明"><Input value={f.description} onChange={(e) => setF((p) => ({ ...p, description: e.target.value }))} placeholder="国内直连，其余走代理" /></Field>
          </div>
        )}
        <Tabs value={kind} onChange={setKind} items={KINDS.map((k) => ({ value: k, label: <span className="inline-flex items-center gap-1.5">{RULE_KIND_LABELS[k]}<span className={cn("h-1.5 w-1.5 rounded-full", f[k].trim() ? "bg-emerald-500" : "bg-muted-foreground/30")} /></span> }))} />
        <Textarea rows={20} spellCheck={false} readOnly={readOnly} className="font-mono text-xs leading-5" value={f[kind]} onChange={(e) => setF((p) => ({ ...p, [kind]: e.target.value }))}
          placeholder={`留空：${RULE_KIND_LABELS[kind]} 用户拿到的是「无规则」配置`} />
        {!readOnly && (
          <div className="flex flex-wrap items-start justify-between gap-3 text-xs text-muted-foreground">
            <p className="min-w-0 flex-1">
              写一份完整的 {RULE_KIND_LABELS[kind]} 配置，节点由面板填进去：<Code>{"{{all}}"}</Code> 展开成这个用户的全部线路（按节点页的顺序），<Code>{"{{all|正则}}"}</Code> 只取名字匹配的
              {kind === "shadowrocket" || kind === "surge" ? <>，<Code>{"{{PROXIES}}"}</Code> 放在 [Proxy] 里。</> : "。"}
              某个分组一条线路都没有时会填成 REJECT，不会变成直连。
            </p>
            {!f[kind].trim() && <Button size="sm" variant="outline" onClick={() => setF((p) => ({ ...p, [kind]: none[kind] }))}>从「无规则」开始改</Button>}
          </div>
        )}
      </div>
    </Dialog>
  );
}
