import test from 'node:test';
import assert from 'node:assert/strict';
import { aggregate } from '../components/fleet/fleetModel.ts';

const cpuOnly = () => ({machine_id:'cpu',hostname:'cpu',last_seen:Date.now(),cpu_percent:40,ram_used_bytes:4,ram_total_bytes:8,disk_used_bytes:2,disk_total_bytes:8,gpus:[],gpu_util_percent:0,gpu_vram_total_bytes:0,gpu_temp:0});
const gpuMachine = () => ({...cpuOnly(),machine_id:'gpu',hostname:'gpu',gpus:[
 {name:'GPU0',util_percent:20,mem_used_bytes:2,mem_total_bytes:8,temp_c:60,power_watts:100},
 {name:'GPU1',util_percent:80,mem_used_bytes:6,mem_total_bytes:8,temp_c:75,power_watts:200},
]});
test('the Overview reports unavailable GPU telemetry for CPU-only and empty fleets',()=>{
 for(const machines of [[],[cpuOnly()]]){
  const result=aggregate(machines);
  for(const key of ['avgGpuUtil','avgVram','maxGpuTemp','gpuPowerTotal'])assert.equal(result[key],null,key);
  assert.deepEqual(result.topGpu,[]);
 }
 assert.equal(aggregate([]).onlinePct,null);
});
test('the Overview uses every GPU and does not dilute GPU averages with CPU-only machines',()=>{
 const result=aggregate([cpuOnly(),gpuMachine()]);
 assert.equal(result.avgGpuUtil,50);assert.equal(result.avgVram,50);
 assert.equal(result.maxGpuTemp,75);assert.equal(result.gpuPowerTotal,300);
 assert.equal(result.topGpu.length,1);assert.equal(result.topGpu[0].machineId,'gpu');
});
test('stale and offline machines cannot contribute to current resource or power readings',()=>{
 const stale={...gpuMachine(),last_seen:Date.now()-40000};
 const offline={...gpuMachine(),machine_id:'offline',last_seen:Date.now()-180000};
 const result=aggregate([stale,offline]);
 for(const key of ['avgCpu','avgRam','avgGpuUtil','avgVram','maxGpuTemp','gpuPowerTotal'])assert.equal(result[key],null,key);
 assert.deepEqual(result.topGpu,[]);
});
test('fleet averages count GPUs, not machine averages, when device counts differ',()=>{
 const second={...gpuMachine(),machine_id:'single',gpus:[{name:'GPU0',util_percent:100,mem_used_bytes:8,mem_total_bytes:8,temp_c:70,power_watts:50}]};
 const result=aggregate([gpuMachine(),second]);
 assert.ok(Math.abs(result.avgGpuUtil-200/3)<1e-9);
 assert.ok(Math.abs(result.avgVram-200/3)<1e-9);
 assert.equal(result.gpuPowerTotal,350);
});
test('legacy GPU power: only >0 is observed; mixed is partial; all-zero/absent is unavailable',()=>{
 // All devices report a positive draw -> complete total.
 const all=aggregate([gpuMachine()]);
 assert.equal(all.gpuPowerTotal,300);
 assert.equal(all.gpuPowerComplete,true);
 // One device omits the reading -> partial, only the observed device counts.
 const missing=gpuMachine();delete missing.gpus[1].power_watts;
 const partialMissing=aggregate([missing]);
 assert.equal(partialMissing.gpuPowerTotal,100);
 assert.equal(partialMissing.gpuPowerComplete,false);
 // A 0 is ambiguous (the agent serializes "N/A" power as 0), so it is not an
 // observation: mixed positive + zero is a partial sum, never complete.
 const mixed=gpuMachine();mixed.gpus[1].power_watts=0;
 const partialZero=aggregate([mixed]);
 assert.equal(partialZero.gpuPowerTotal,100);
 assert.equal(partialZero.gpuPowerComplete,false);
 // Every device reads 0 -> entirely ambiguous -> unavailable, never a 0 W total.
 const allZero=gpuMachine();allZero.gpus.forEach(g=>{g.power_watts=0;});
 const zeroResult=aggregate([allZero]);
 assert.equal(zeroResult.gpuPowerTotal,null);
 assert.equal(zeroResult.gpuPowerComplete,false);
 // Non-power GPU readings are unaffected: 0% utilization stays a valid reading.
 const idle=gpuMachine();idle.gpus.forEach(g=>{g.util_percent=0;});
 assert.equal(aggregate([idle]).avgGpuUtil,0);
});
test('stale machines count as connected but are surfaced separately, never as reporting',()=>{
 const stale={...gpuMachine(),last_seen:Date.now()-60000};
 // All stale: connected but every one is stale, so no "all reporting".
 const allStale=aggregate([stale]);
 assert.equal(allStale.total,1);
 assert.equal(allStale.online,1);
 assert.equal(allStale.stale,1);
 assert.equal(allStale.onlinePct,100);
 // Mixed fresh + stale: both connected, one stale.
 const fresh={...gpuMachine(),machine_id:'fresh',last_seen:Date.now()};
 const mixed=aggregate([fresh,stale]);
 assert.equal(mixed.total,2);
 assert.equal(mixed.online,2);
 assert.equal(mixed.stale,1);
 // Offline is neither connected nor stale.
 const offline={...gpuMachine(),machine_id:'off',last_seen:Date.now()-180000};
 const withOffline=aggregate([fresh,stale,offline]);
 assert.equal(withOffline.total,3);
 assert.equal(withOffline.online,2);
 assert.equal(withOffline.stale,1);
});
test('non-finite readings are unavailable, not NaN output',()=>{
 const machine={...cpuOnly(),cpu_percent:NaN,ram_used_bytes:Infinity};
 const result=aggregate([machine]);assert.equal(result.avgCpu,null);assert.equal(result.avgRam,null);
});
