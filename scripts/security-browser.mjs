// Real Chromium integration against ephemeral HTTPS origins and an empty DB.
// Usage: CTLVPS_TEST_BINARY=... PLAYWRIGHT_MODULE_DIR=... node scripts/security-browser.mjs
import {createRequire} from 'node:module';import fs from 'node:fs';import os from 'node:os';import path from 'node:path';import https from 'node:https';import http from 'node:http';import {spawn,execFileSync} from 'node:child_process';import assert from 'node:assert/strict';
const require=createRequire(import.meta.url);const {chromium}=require(process.env.PLAYWRIGHT_MODULE_DIR ? path.join(process.env.PLAYWRIGHT_MODULE_DIR,'playwright'): 'playwright');
const dir=fs.mkdtempSync(path.join(os.tmpdir(),'ctlvps-browser-'));let child,browser,front;const violations=[];
const listen=s=>new Promise(r=>s.listen(0,'127.0.0.1',()=>r(s.address().port)));
try{
 execFileSync('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-days','1','-keyout',dir+'/tls.key','-out',dir+'/tls.crt','-subj','/CN=panel.fixture.test'],{stdio:'ignore'});
 const reserve=http.createServer();const backPort=await listen(reserve);await new Promise(r=>reserve.close(r));
 front=https.createServer({key:fs.readFileSync(dir+'/tls.key'),cert:fs.readFileSync(dir+'/tls.crt')},(req,res)=>{
  if(!req.headers.host.startsWith('panel.fixture.test:')){res.setHeader('Content-Type','text/html');res.end('<!doctype html><title>Hostile fixture</title><body>Independent origin fixture</body>');return;}
  const proxy=http.request({hostname:'127.0.0.1',port:backPort,path:req.url,method:req.method,headers:req.headers},out=>{res.writeHead(out.statusCode,out.headers);out.pipe(res)});proxy.on('error',()=>{res.statusCode=503;res.end()});req.pipe(proxy);
 });
 const port=await listen(front),origin=`https://panel.fixture.test:${port}`;
 fs.mkdirSync(dir+'/data',{mode:0o700});const token=Buffer.alloc(32,7).toString('base64url');fs.writeFileSync(dir+'/data/setup-token',token,{mode:0o600});
 const log=fs.openSync(dir+'/server.log','w');child=spawn(process.env.CTLVPS_TEST_BINARY??'./bin/ctlvpsd',['--data',dir+'/data','--listen',`127.0.0.1:${backPort}`,'--site-url',origin],{stdio:['ignore',log,log]});
 for(let i=0;i<80;i++){try{const r=await fetch(`http://127.0.0.1:${backPort}/healthz`);if(r.ok)break}catch{}await new Promise(r=>setTimeout(r,100));}
 browser=await chromium.launch({headless:true,...(process.env.CTLVPS_BROWSER_CHANNEL?{channel:process.env.CTLVPS_BROWSER_CHANNEL}:{}),args:['--no-proxy-server','--host-resolver-rules=MAP panel.fixture.test 127.0.0.1, MAP evil.fixture.test 127.0.0.1, MAP evil.other.test 127.0.0.1']});
 const context=await browser.newContext({ignoreHTTPSErrors:true});const page=await context.newPage();page.on('pageerror',e=>violations.push(e.message));
 await page.goto(origin);await page.getByRole('button',{name:'创建管理员并登录'}).waitFor();
 await page.getByLabel('初始化令牌').fill(token);await page.locator('input[autocomplete="username"]').fill('browser-fixture');const passwords=page.locator('input[autocomplete="new-password"]');await passwords.nth(0).fill('fixture-password-123');await passwords.nth(1).fill('fixture-password-123');await page.getByRole('button',{name:'创建管理员并登录'}).click();
 await page.waitForURL(url=>!url.pathname.includes('login'));await page.goto(origin+'/servers');await page.getByRole('button',{name:'添加服务器',exact:true}).first().click();await page.getByPlaceholder('hk-1').fill('safe-browser-fixture');await page.getByRole('button',{name:'保存',exact:true}).click();await page.getByText('safe-browser-fixture',{exact:true}).first().waitFor();
 const cookies=await context.cookies(origin);const session=cookies.find(c=>c.name==='__Host-ctlvps_session');assert(session?.secure&&session.httpOnly&&session.path==='/'&&session.sameSite==='Lax');
 const inspect=()=>page.evaluate(async()=>({me:await (await fetch('/api/v1/auth/me')).json(),servers:await (await fetch('/api/v1/servers')).json()}));const before=await inspect();
 // Same-site subdomains receive SameSite cookies on their requests to panel,
 // so the Origin/CSRF boundary, not just SameSite, must reject these attempts.
 for(const hostname of ['evil.fixture.test','evil.other.test']){
  const evil=await context.newPage();const frameErrors=[];evil.on('console', m=>{if(m.type()==='error')frameErrors.push(m.text())});await evil.goto(`https://${hostname}:${port}`);
  const blocked=await evil.evaluate(async(target)=>{
   const outcomes=[];for(const contentType of ['text/plain','application/x-www-form-urlencoded']){try{const r=await fetch(target+'/api/v1/servers',{method:'POST',credentials:'include',headers:{'Content-Type':contentType},body:'{"name":"attacker-created"}'});outcomes.push(r.status)}catch{outcomes.push('cors-blocked')}}
   try{await fetch(target+'/api/v1/servers',{method:'POST',credentials:'include',headers:{'Content-Type':'application/json'},body:'{"name":"attacker-json"}'})}catch{outcomes.push('preflight-blocked')}
   const frame=document.createElement('iframe');frame.src=target;document.body.append(frame);return outcomes;
  },origin);
  assert(blocked.includes('preflight-blocked'));await evil.waitForTimeout(300);assert(frameErrors.some(s=>/frame-ancestors|X-Frame-Options|refused to frame/i.test(s)), 'iframe must be blocked by browser');await evil.close();
 }
 const missing=await page.evaluate(async()=>{const r=await fetch('/api/v1/auth/profile',{method:'PUT',headers:{'Content-Type':'application/json'},body:'{"nickname":"tampered"}'});return r.status});assert.equal(missing,403);
 const csp=await page.evaluate(()=>new Promise(resolve=>{window.__injected=false;const el=document.createElement('script');el.textContent='window.__injected=true';document.body.append(el);setTimeout(()=>resolve(window.__injected),100)}));assert.equal(csp,false);
 const after=await inspect();assert.equal(after.servers.length,before.servers.length);assert.equal(after.me.nickname,before.me.nickname);assert.equal(violations.length,0);
 await page.goto(origin+'/servers/'+after.servers[0].id);await page.goto(origin+'/nodes');await page.getByRole('button',{name:'新建入站',exact:true}).first().click();const dialog=page.getByRole('dialog');await dialog.getByRole('button',{name:'创建',exact:true}).click();await dialog.waitFor({state:'hidden'});await page.getByText('443',{exact:true}).first().waitFor();await page.goto(origin+'/servers/'+after.servers[0].id);
 // A manually recovered agent retains failed history without a stale banner.
 const maintenanceURL=`**/api/v1/servers/${after.servers[0].id}/maintenance`;
 const failedJob={id:'fixture-maintenance',role:'agent',action:'update',status:'failed',version:'v0.1.5',message:'fixture download failure',updated_at:new Date().toISOString()};
 const maintenanceState={available:true,version:'v0.1.5',target_version:'v0.1.5',agent_update:{current_sha:'same',latest_sha:'same',outdated:false},jobs:[failedJob]};
 await page.route(maintenanceURL,route=>route.fulfill({json:maintenanceState}));
 await page.reload();await page.getByRole('button',{name:'更多',exact:true}).click();await page.getByText('agent 维护',{exact:true}).click();
 await page.getByText('操作记录',{exact:true}).click();await page.getByText('fixture download failure',{exact:true}).waitFor();
 assert.equal(await page.getByRole('status').filter({hasText:'agent 升级'}).count(),0);
 await page.keyboard.press('Escape');
 maintenanceState.agent_update.current_sha='old';maintenanceState.agent_update.outdated=true;
 await page.reload();await page.getByRole('status').filter({hasText:'agent 升级 · 失败'}).waitFor();
 maintenanceState.agent_update.current_sha='same';maintenanceState.agent_update.outdated=false;maintenanceState.available=false;
 await page.reload();await page.getByRole('status').filter({hasText:'agent 升级 · 失败'}).waitFor();
 maintenanceState.available=true;failedJob.action='uninstall';
 await page.reload();await page.getByRole('status').filter({hasText:'agent 卸载 · 失败'}).waitFor();
 await page.unroute(maintenanceURL);
 console.log('PASS recovered agent hides obsolete update banner, retains history, and preserves offline/outdated/uninstall failures.');
 // Release smoke: a line, a user and that user's own page, created through the interface.
 await page.goto(origin+'/nodes');
 await page.getByRole('button',{name:'新建线路',exact:true}).click();
 const lineDialog=page.getByRole('dialog');
 await lineDialog.getByRole('combobox').nth(1).click();
 await page.getByRole('option',{name:/safe-browser-fixture/}).first().click();
 await lineDialog.getByPlaceholder('🇺🇸 洛杉矶').fill('fixture-line');
 await lineDialog.getByRole('button',{name:'创建',exact:true}).click();
 await lineDialog.waitFor({state:'hidden'});
 await page.getByText('fixture-line',{exact:true}).first().waitFor();
 await page.goto(origin+'/users');
 await page.getByRole('button',{name:'新建用户',exact:true}).first().click();
 const userDialog=page.getByRole('dialog');
 await userDialog.getByPlaceholder('sansan').fill('fixture-user');
 await userDialog.getByRole('button',{name:'创建',exact:true}).click();
 await page.getByText('专属链接',{exact:true}).waitFor();
 await page.getByText('fixture-line',{exact:true}).first().waitFor();
 const personalPath=new URL((await page.locator('code').first().textContent()).trim()).pathname;
 assert(/^\/r\/[A-Za-z0-9]{24}$/.test(personalPath),'a user link must be the short personal address');
 const personal=origin+personalPath;
 for(const route of ['/','/servers','/nodes','/users','/rules','/connlog']) {
  await page.setViewportSize({width:390,height:844});
  await page.goto(origin+route);await page.getByRole('heading').first().waitFor();
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),route+' overflows a phone viewport');
 }
 // The personal page needs no session: open it in a clean context.
 const guest=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:390,height:844}});
 const guestPage=await guest.newPage();const guestErrors=[];guestPage.on('console',m=>{if(m.type()==='error')guestErrors.push(m.text())});
 await guestPage.goto(personal);
 await guestPage.getByRole('heading',{name:'fixture-user'}).waitFor();
 await guestPage.getByRole('heading',{name:'一键导入'}).waitFor();
 await guestPage.getByText('fixture-line',{exact:true}).waitFor();
 assert(await guestPage.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'personal page overflows a phone viewport');
 assert.equal(guestErrors.filter(e=>/Content Security Policy/i.test(e)).length,0,'personal page violates CSP');
 await guest.close();
 assert.equal(violations.length,0);
 console.log('PASS release smoke: line and user created in the interface, six pages fit 390px, personal page opens without a session.');
 await page.screenshot({path:'/tmp/ctlvps-security-browser.png',fullPage:true});console.log('PASS Chromium: UI setup/create, Secure HttpOnly host cookie, same-site/cross-site attacks, CSRF and CSP; no unauthorized mutation.');
}finally{await browser?.close();child?.kill();await new Promise(r=>front?front.close(r):r());fs.rmSync(dir,{recursive:true,force:true});}
