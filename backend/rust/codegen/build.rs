fn main() {
    tonic_build::configure()
        .out_dir("../bunker/generated")
        .build_server(true)
        .build_client(false)
        .compile(
            &["../../../pkg/grpc/proto/backend.proto"],
            &["../../../pkg/grpc/proto"],
        )
        .unwrap();
}
