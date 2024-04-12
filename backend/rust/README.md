## Here is a backend written in Rust for the LocalAI project

Here are some rules for the Rust backend:
* Same proto file with the LocalAI's other backends, we should keep the same interface of the backend.
* `async` should be as the default way to write code.
* Streaming response should be supported.
* Only server side gRPC services are supported for current backend.
* The backend should also have metrics for monitoring.


### The information of the environment

* cargo 1.73.0 (9c4383fb5 2023-08-26)
* rustup 1.26.0 (5af9b9484 2023-04-05)
* rustc 1.73.0 (cc66ad468 2023-10-03)

## Build the development environment

#### Protocol Buffers compiler

Ubuntu or Debian

```
sudo apt update && sudo apt upgrade -y
sudo apt install -y protobuf-compiler libprotobuf-dev
```

macOS
```
brew install protobuf
```

### Cargo fmt all the code

```
cargo fmt --all --check
```

### Check the gRPC backend status

It will return base64 encoded string of the `OK`.


```bash
make burn

make test
```

```
{
  "message": "T0s="
}
```
