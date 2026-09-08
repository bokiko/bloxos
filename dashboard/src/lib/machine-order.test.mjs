import test from 'node:test';
import assert from 'node:assert/strict';
import { orderMachines, moveMachine, normalizeMachineOrder, acceptedMachineOrder, createPreferenceWriter } from './machine-order.mjs';
const fleet = [{machine_id:'b',hostname:'Beta',cpu_percent:1},{machine_id:'a',hostname:'Alpha',cpu_percent:99},{machine_id:'c',hostname:'Gamma',cpu_percent:20}];
const ids = list => list.map(m => m.machine_id);
test('name order does not move when status, connectivity or metrics change', () => {
  const changed = fleet.map(m => ({...m,cpu_percent:100-(m.cpu_percent??0),last_seen:0})).reverse();
  assert.deepEqual(ids(orderMachines(fleet)), ['a','b','c']);
  assert.deepEqual(ids(orderMachines(changed,{status:()=>999})), ['a','b','c']);
  assert.deepEqual(ids(fleet), ['b','a','c']);
});
test('manual order beats telemetry, names, pins and incoming SSE order', () => {
  for(const list of [fleet,[...fleet].reverse(),fleet.map(m=>({...m,hostname:'renamed',cpu_percent:0}))])
    assert.deepEqual(ids(orderMachines(list,{sort:'manual',order:['c','a','b'],pinned:['b'],status:()=>100})), ['c','a','b']);
});
test('new machines append, deleted IDs disappear, filtered machines retain positions', () => {
  const options={sort:'manual',order:['gone','c','a']};
  assert.deepEqual(ids(orderMachines(fleet,options)),['c','a','b']);
  assert.deepEqual(ids(orderMachines(fleet.filter(m=>m.machine_id!=='a'),options)),['c','b']);
});
test('ties are deterministic and explicit live sort still works', () => {
  assert.deepEqual(ids(orderMachines(fleet,{sort:'cpu'})),['a','c','b']);
  assert.deepEqual(ids(orderMachines(fleet,{sort:'status',status:m=>m.machine_id==='b'?0:1})),['b','a','c']);
  const same=fleet.map(m=>({...m,hostname:'same'}));
  assert.deepEqual(ids(orderMachines(same.reverse())),['a','b','c']);
});
test('pins only reorder non-manual views after explicit pin action', () => {
  assert.deepEqual(ids(orderMachines(fleet,{pinned:['c']})),['c','a','b']);
});
test('moves work in both directions without duplicates or mutation', () => {
  const order=['a','b','c'];
  assert.deepEqual(moveMachine(order,'a','c'),['b','c','a']);
  assert.deepEqual(moveMachine(order,'c','a'),['c','a','b']);
  assert.deepEqual(moveMachine(order,'missing','a'),order);
  assert.deepEqual(moveMachine(order,'a','a'),order);
  assert.deepEqual(order,['a','b','c']);
});
test('corrupt cached order cannot create phantom entries or duplicates', () => {
  assert.deepEqual(normalizeMachineOrder(['a',null,'a','',3,'b']),['a','b']);
  assert.deepEqual(normalizeMachineOrder({}),[]);
});
test('save only succeeds for an exact server acknowledgment, including empty order', () => {
  assert.equal(acceptedMachineOrder({default_sort:'manual',machine_order:['b','a']},['b','a']),true);
  assert.equal(acceptedMachineOrder({default_sort:'manual',machine_order:[]},[]),true);
  for(const bad of [{},{default_sort:'name',machine_order:['b','a']},{default_sort:'manual',machine_order:['a','b']},null])
    assert.equal(acceptedMachineOrder(bad,['b','a']),false);
});
test('preference writes serialize and a failure does not block retry', async () => {
  const write = createPreferenceWriter();
  const seen=[];
  let release;
  const gate=new Promise(resolve=>{release=resolve});
  const first=write(async()=>{seen.push('first');await gate;throw new Error('offline')});
  const rejected=assert.rejects(first,/offline/);
  const second=write(async()=>{seen.push('second');return 'saved'});
  await Promise.resolve();
  assert.deepEqual(seen,['first']);
  release();
  await rejected;
  assert.equal(await second,'saved');
  assert.deepEqual(seen,['first','second']);
});
