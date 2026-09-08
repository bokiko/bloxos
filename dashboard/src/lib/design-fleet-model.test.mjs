import test from 'node:test';
import assert from 'node:assert/strict';
import { aggregate } from '../components/fleet/fleetModel.ts';

const cpuOnly = () => ({machine_id:'cpu',hostname:'cpu',last_seen:Date.now(),cpu_percent:40,ram_used_bytes:4,ram_total_bytes:8,disk_used_bytes:2,disk_total_bytes:8,gpus:[],gpu_util_percent:0,gpu_vram_total_bytes:0,gpu_temp:0});
const gpuMachine = () => ({...cpuOnly(),machine_id:'gpu',hostname:'gpu',gpus:[
 {name:'GPU0',util_percent:20,mem_used_bytes:2,mem_total_bytes:8,temp_c:60,power_watts:100},
 {name:'GPU1',util_percent:80,mem_used_bytes:6,mem_total_bytes:8,temp_c:75,power_watts:200},
]});
test('new layouts report unavailable GPU telemetry for CPU-only and empty fleets',()=>{
 for(const machines of [[],[cpuOnly()]]){
  const result=aggregate(machines);
  for(const key of ['avgGpuUtil','avgVram','maxGpuTemp','gpuPowerTotal'])assert.equal(result[key],null,key);
  assert.deepEqual(result.topGpu,[]);assert.deepEqual(result.topVram,[]);
 }
 assert.equal(aggregate([]).onlinePct,null);
});
test('new layouts use every GPU and do not dilute GPU averages with CPU-only machines',()=>{
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
 assert.deepEqual(result.topVram,[]);
});
test('fleet averages count GPUs, not machine averages, when device counts differ',()=>{
 const second={...gpuMachine(),machine_id:'single',gpus:[{name:'GPU0',util_percent:100,mem_used_bytes:8,mem_total_bytes:8,temp_c:70,power_watts:50}]};
 const result=aggregate([gpuMachine(),second]);
 assert.ok(Math.abs(result.avgGpuUtil-200/3)<1e-9);
 assert.ok(Math.abs(result.avgVram-200/3)<1e-9);
 assert.equal(result.gpuPowerTotal,350);
});
test('partial GPU power must not be presented as a complete fleet total; valid zero survives',()=>{
 const machine=gpuMachine();delete machine.gpus[1].power_watts;
 const partial=aggregate([machine]);
 assert.equal(partial.gpuPowerComplete,false);
 assert.equal(partial.gpuPowerTotal,100);
 machine.gpus.forEach(g=>{g.power_watts=0;g.util_percent=0;});
 assert.equal(aggregate([machine]).gpuPowerTotal,0);
 assert.equal(aggregate([machine]).avgGpuUtil,0);
});
test('non-finite readings are unavailable, not NaN output',()=>{
 const machine={...cpuOnly(),cpu_percent:NaN,ram_used_bytes:Infinity};
 const result=aggregate([machine]);assert.equal(result.avgCpu,null);assert.equal(result.avgRam,null);
});
