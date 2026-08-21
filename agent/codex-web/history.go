package codexweb

// history.go —— thread/read(includeTurns) → rich history → pathless hydrate（§7/§9）。
//
// 一期稳定基线 = thread/read{includeTurns:true}（只读，不 resume）；thread/turns/list
// 仅 experimental 门控后用于分页优化（§11.2）。Phase 0 样本：dumps/catalog。
