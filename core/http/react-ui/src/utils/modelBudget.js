// modelBudget answers the one question every "will this model run here" verdict
// on the models page is built from: how much memory a model may occupy, and
// whose memory it is.
//
// In distributed mode that is NOT the host serving this page. The controller is
// usually a GPU-less pod while every model runs on a worker, so sizing against
// its own aggregate told admins that a cluster of A100s could only run the
// smallest CPU build. The server reports the cluster's best single node in an
// additional `cluster` block; the local aggregate stays untouched for the
// resource monitor, which is genuinely about this host.
//
// The best single node, not the fleet total: a model loads into one node, so a
// summed fleet of four 16GB cards would promise a 40GB model a home it does not
// have.
//
// Every missing or unusable field falls back to the local reading, so a
// controller that cannot reach its registry keeps behaving exactly as a
// single-node install does.
export function modelBudget(resources) {
  const cluster = resources?.cluster
  if (cluster?.enabled && cluster.total_memory > 0) {
    return {
      totalMemory: cluster.total_memory,
      hasGpu: !!cluster.is_gpu,
      nodeName: cluster.node_name || '',
      nodeCount: cluster.node_count || 0,
      scope: 'cluster',
    }
  }

  return {
    totalMemory: resources?.aggregate?.total_memory || 0,
    // gpu_count is 0 and gpus is null on a CPU-only host, where total_memory is
    // system RAM. The fits check has always used it either way; only the copy
    // has to stop calling it VRAM.
    hasGpu: (resources?.aggregate?.gpu_count || 0) > 0 || (resources?.gpus?.length || 0) > 0,
    nodeName: '',
    nodeCount: 0,
    scope: 'local',
  }
}
