// Package p2psplit runs model splitting over P2P: llama.cpp RPC workers and
// MLX ring workers that lend their memory to a model that runs on the server.
//
// A split worker pairs with the server once, with an invite of kind split
// that names the engine. It then keeps a control session open to the server
// (ProtoControl). The server opens the data streams over the connection the
// worker keeps: one ProtoLlamaRPC stream per llama.cpp RPC connection, and
// ProtoMLX streams for the MLX ring.
//
// Server side, Manager forwards a stable loopback port to each worker that
// can carry model traffic and hands the addresses to the engines (the
// LLAMACPP_GRPC_SERVERS list and the MLX hostfile). Model traffic dials with
// p2pnet.NeedBulk, so a relay with a data limit (a public relay) never
// carries it. A worker reached only through a relay without such a limit is
// slow: the server leaves it out until the administrator chooses "Use
// anyway". A direct or hole-punched path is always used.
//
// MLX ring ranks talk to their neighbours. Workers have no link to each
// other: every ring stream goes through the server, which relays a stream
// between two workers. Rank 0 is the MLX process of the server; the ranks of
// the workers follow the pairing order. Only the ring backend works this
// way; JACCL needs RDMA between the machines.
//
// Worker side, Agent keeps the control session, serves the data streams and,
// for MLX, writes the hostfile of its rank and tells the caller to (re)start
// the MLX process.
//
// The package imports no libp2p package; it uses p2pnet.
package p2psplit
