import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {userIDFromToken} from './auth-session.mjs';

test('actual inline bootstrap selects base64url account cache, never dirty guest cache', () => {
  const layout = readFileSync(new URL('../app/layout.tsx', import.meta.url),'utf8');
  const source = layout.match(/const themeBootstrapScript = `([\s\S]*?)`\.trim\(\);/)[1];
  let sawURLAlphabet = false;
  for (const username of ['x>', 'x?', '🦊', 'مستخدم']) {
    const user = 'account-'+username;
    const payload = Buffer.from(JSON.stringify({user_id:user,username,exp:2000000000})).toString('base64url');
    sawURLAlphabet ||= /[-_]/.test(payload);
    const token = `header.${payload}.signature`;
    assert.equal(userIDFromToken(token),user);
    const items = new Map([
      ['bloxos_token',token],
      ['bloxos-design:'+user,JSON.stringify({layout:'grove',colors:{grove:'bright'}})],
      ['bloxos-design:guest',JSON.stringify({layout:'console',colors:{console:'dark'},pendingSync:true})],
    ]);
    const root={dataset:{},style:{},classList:{add(){},remove(){}}};
    runInNewContext(source,{atob,TextDecoder,Uint8Array,localStorage:{getItem:key=>items.get(key)??null},document:{documentElement:root},window:{matchMedia:()=>({matches:true})}});
    assert.equal(root.dataset.layout,'grove');
    assert.equal(root.dataset.designColor,'bright');
  }
  assert.ok(sawURLAlphabet,'fixtures must exercise - or _ in the JWT payload');
});
