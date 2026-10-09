import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { get, put } from "@/lib/api";
import type { RuleList, Rules } from "@/lib/types";
import { fmtAgo } from "@/lib/utils";
import { Button, Card, Code, Field, Input, PageHeader, Spinner, Textarea } from "@/components/ui";
import { useToast } from "@/components/toast";

const LIST_KIND: Record<RuleList["kind"], string> = { domain: "域名", ipcidr: "IP 段", classical: "软件" };

/** How many lines of the rules are rules, not remarks. */
function ruleCount(rules: string): number {
  return rules.split("\n").filter((l) => l.trim() && !/^(#|;|\/\/)/.test(l.trim())).length;
}

export function RulesPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["rules"], queryFn: () => get<Rules>("/api/v1/rules") });
  const [rules, setRules] = React.useState("");
  const [group, setGroup] = React.useState("");
  // What the panel said was wrong with the rules, kept in view until they are edited.
  const [error, setError] = React.useState("");
  const loaded = q.data;
  React.useEffect(() => {
    if (loaded) { setRules(loaded.rules); setGroup(loaded.group_name); setError(""); }
  }, [loaded]);
  const save = useMutation({
    mutationFn: () => put<Rules>("/api/v1/rules", { rules, group_name: group }),
    onSuccess: (r) => { qc.setQueryData(["rules"], r); toast.success("规则已保存，大家下次更新配置时生效"); },
    onError: (e) => setError(e instanceof Error ? e.message : String(e)),
  });
  if (q.isLoading || !loaded) return <Spinner />;
  const dirty = rules !== loaded.rules || group.trim() !== loaded.group_name;
  return (
    <div>
      <PageHeader title="规则" description="整个面板只有这一份规则：每个用户、Clash 和小火箭都用它。改了以后，客户端下次更新配置时一起变" />
      <Card className="space-y-4 p-5">
        <Textarea rows={22} spellCheck={false} className="font-mono text-xs leading-5" value={rules} onChange={(e) => { setRules(e.target.value); setError(""); }}
          placeholder={"# 一行一条，从上往下匹配，先中先得\nRULE-SET,direct,DIRECT\nRULE-SET,cncidr,DIRECT\nMATCH,PROXY"} />
        {error && <p role="alert" className="rounded-xl border border-red-500/40 bg-red-500/5 px-4 py-3 text-sm text-red-600 dark:text-red-400">没有保存：{error}</p>}
        <div className="flex flex-wrap items-end justify-between gap-3">
          <Field label="线路组的名字" hint="Clash 类客户端里那个选线路的分组叫什么。客户端按这个名字记着每个人上次选的线路，改名后大家会回到第一条。">
            <Input value={group} onChange={(e) => { setGroup(e.target.value); setError(""); }} maxLength={64} />
          </Field>
          <div className="flex items-center gap-3">
            <span className="text-xs text-muted-foreground">
              {dirty ? "有改动还没保存" : loaded.saved ? `${ruleCount(loaded.rules)} 条规则 · ${fmtAgo(loaded.updated_at)}修改` : "还没改过，现在生效的是内置的最简规则"}
            </span>
            {dirty && <Button variant="outline" onClick={() => { setRules(loaded.rules); setGroup(loaded.group_name); setError(""); }}>放弃改动</Button>}
            <Button onClick={() => save.mutate()} loading={save.isPending} disabled={!dirty}>保存</Button>
          </div>
        </div>
        <div className="space-y-2 border-t pt-4 text-xs text-muted-foreground">
          <p>
            一行一条：<Code>类型,内容,动作</Code>，从上往下匹配。动作只有三种：<Code>PROXY</Code> 走用户选的线路，<Code>DIRECT</Code> 直连，<Code>REJECT</Code> 拒绝。
            类型可以是 <Code>DOMAIN</Code>、<Code>DOMAIN-SUFFIX</Code>、<Code>DOMAIN-KEYWORD</Code>、<Code>IP-CIDR</Code>、<Code>IP-CIDR6</Code>、<Code>GEOIP</Code>、<Code>RULE-SET</Code>（下面的名单）；
            最后一行写 <Code>MATCH,动作</Code> 表示其余的怎么办，不写就是 <Code>MATCH,PROXY</Code>。按 IP 匹配的规则后面可以加 <Code>,no-resolve</Code>。<Code>#</Code> 开头的是注释。保存时逐行检查，写错的那一行会指出来。
          </p>
          <p>
            Clash 里 <Code>PROXY</Code> 是上面那个线路组，小火箭里是首页点中的那条线路。线路由面板按每个用户填进去；用户没有线路时 <Code>PROXY</Code> 一律变成拒绝，不会变成直连。
            全部清空再保存，就回到内置的最简规则：局域网直连，其余走所选线路。
          </p>
          <div className="flex flex-wrap gap-x-3 gap-y-1">
            <span>可用的名单：</span>
            {loaded.lists.map((l) => <span key={l.name} title={l.about}><Code>{l.name}</Code> {l.about.split("（")[0]}（{LIST_KIND[l.kind]}）</span>)}
          </div>
        </div>
      </Card>
    </div>
  );
}
