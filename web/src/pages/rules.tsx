import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Star, Trash2 } from "lucide-react";
import { del, get, post, put } from "@/lib/api";
import type { RuleList, Ruleset, RulesetList } from "@/lib/types";
import { fmtAgo } from "@/lib/utils";
import { Badge, Button, Card, Code, Confirm, Dialog, Field, Input, PageHeader, Spinner, Textarea } from "@/components/ui";
import { useToast } from "@/components/toast";

/** How many lines of a rule set are rules, not remarks. */
function ruleCount(rules: string): number {
  return rules.split("\n").filter((l) => l.trim() && !/^(#|;|\/\/)/.test(l.trim())).length;
}

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
      <PageHeader title="规则" description="规则只写一份，Clash 和小火箭的配置都由它生成；改了规则，两边下次更新配置时一起变" actions={<Button onClick={() => setDialog({})}><Plus className="h-4 w-4" /> 新建规则</Button>} />
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
            <p className="mt-3 text-xs text-muted-foreground">{ruleCount(r.rules)} 条规则 · {r.users} 个用户在用 · {fmtAgo(r.updated_at)}修改</p>
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
          <p className="mt-3 text-xs text-muted-foreground">{d.none_users} 个用户在用</p>
          <div className="mt-auto flex justify-end gap-1 pt-3">
            {d.default_id !== 0 && <Button size="sm" variant="ghost" onClick={() => setDefault.mutate(0)}><Star className="h-4 w-4" /> 设为默认</Button>}
            <Button size="sm" variant="ghost" onClick={() => setView(true)}>查看</Button>
          </div>
        </Card>
      </div>
      <RuleDialog state={dialog} data={d} onClose={() => setDialog(null)} onSaved={invalidate} />
      <RuleDialog state={view ? {} : null} data={d} readOnly onClose={() => setView(false)} onSaved={invalidate} />
      <Confirm open={!!confirm} onClose={() => setConfirm(null)} onConfirm={() => confirm && remove.mutate(confirm)} loading={remove.isPending} destructive title={`删除规则「${confirm?.name ?? ""}」？`} description={`${confirm?.users ?? 0} 个用户会在下次更新配置时变成「无规则」。`} />
    </div>
  );
}

const LIST_KIND: Record<RuleList["kind"], string> = { domain: "域名", ipcidr: "IP 段", classical: "软件" };

type Draft = { name: string; description: string; rules: string; group_name: string };

function RuleDialog({ state, data, readOnly, onClose, onSaved }: { state: { rule?: Ruleset } | null; data: RulesetList; readOnly?: boolean; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const rule = state?.rule;
  const none = data.none;
  const [f, setF] = React.useState<Draft>({ name: "", description: "", rules: "", group_name: "" });
  React.useEffect(() => {
    if (!state) return;
    if (readOnly) setF({ name: "无规则", description: "", ...none });
    else setF(rule ? { name: rule.name, description: rule.description, rules: rule.rules, group_name: rule.group_name } : { name: "", description: "", rules: "", group_name: "" });
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
        <Textarea rows={18} spellCheck={false} readOnly={readOnly} className="font-mono text-xs leading-5" value={f.rules} onChange={(e) => setF((p) => ({ ...p, rules: e.target.value }))}
          placeholder={"# 一行一条，从上往下匹配，先中先得\nRULE-SET,direct,DIRECT\nRULE-SET,cncidr,DIRECT\nMATCH,PROXY"} />
        {!readOnly && (
          <>
            <div className="space-y-2 text-xs text-muted-foreground">
              <p>
                一行一条：<Code>类型,内容,动作</Code>，从上往下匹配。动作只有三种：<Code>PROXY</Code> 走用户选的线路，<Code>DIRECT</Code> 直连，<Code>REJECT</Code> 拒绝。
                类型可以是 <Code>DOMAIN</Code>、<Code>DOMAIN-SUFFIX</Code>、<Code>DOMAIN-KEYWORD</Code>、<Code>IP-CIDR</Code>、<Code>IP-CIDR6</Code>、<Code>GEOIP</Code>、<Code>RULE-SET</Code>（下面的名单）；
                最后一行写 <Code>MATCH,动作</Code> 表示其余的怎么办，不写就是 <Code>MATCH,PROXY</Code>。按 IP 匹配的规则后面可以加 <Code>,no-resolve</Code>。<Code>#</Code> 开头的是注释。
              </p>
              <p>
                这一份规则同时用在 Clash 和小火箭上：Clash 里 <Code>PROXY</Code> 是下面那个线路组，小火箭里是首页点中的那条线路。线路由面板按每个用户填进去，用户没有线路时 <Code>PROXY</Code> 一律变成拒绝，不会变成直连。
              </p>
              <div className="flex flex-wrap gap-x-3 gap-y-1">
                <span>可用的名单：</span>
                {data.lists.map((l) => <span key={l.name} title={l.about}><Code>{l.name}</Code> {l.about.split("（")[0]}（{LIST_KIND[l.kind]}）</span>)}
              </div>
            </div>
            <div className="flex flex-wrap items-end justify-between gap-3">
              <Field label="线路组的名字" hint="Clash 类客户端里那个选线路的分组叫什么。客户端按这个名字记住用户上次选的线路，改名后大家会回到第一条。">
                <Input value={f.group_name} onChange={(e) => setF((p) => ({ ...p, group_name: e.target.value }))} placeholder={none.group_name} maxLength={64} />
              </Field>
              {!f.rules.trim() && <Button size="sm" variant="outline" onClick={() => setF((p) => ({ ...p, rules: none.rules }))}>从「无规则」开始改</Button>}
            </div>
          </>
        )}
      </div>
    </Dialog>
  );
}
