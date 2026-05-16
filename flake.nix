# Made by Azteczek
{
  description = "LocalAI flake";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    inference-defaults = {
      url = "https://raw.githubusercontent.com/unslothai/unsloth/main/studio/backend/assets/configs/inference_defaults.json";
      flake = false;
    };
  };

  outputs = { self, nixpkgs, inference-defaults }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
    in {
      packages.${system}.default = pkgs.buildGoModule {
        pname = "localai";
        version = "custom";
        
 	src = ./sources;
        proxyVendor = true;
        vendorHash = "sha256-MdadwbUc2pwfpC9ScsiIfjGIcAOgcwSm6rt/KNlTIuA=";

        nativeBuildInputs = with pkgs; [ 
          pkg-config cmake gcc protobuf go-protobuf protoc-gen-go protoc-gen-go-grpc
        ];

        env = {
          CGO_ENABLED = "0";
        };

        preBuild = ''
          
          PROTO_SOURCE_DIR=$(find . -name "*.proto" -printf "%h" -quit)
          mkdir -p pkg/grpc/proto
          ${pkgs.protobuf}/bin/protoc \
            -I=$PROTO_SOURCE_DIR \
            -I. \
            --go_out=pkg/grpc/proto --go_opt=paths=source_relative \
            --go-grpc_out=pkg/grpc/proto --go-grpc_opt=paths=source_relative \
            $PROTO_SOURCE_DIR/*.proto

          go mod edit -replace github.com/mudler/LocalAI/pkg/grpc/proto=./pkg/grpc/proto
          
          mkdir -p core/config/gen_inference_defaults
          cp ${inference-defaults} core/config/gen_inference_defaults/inference_defaults.json
          sed -i '/go:generate/d' core/config/inference_defaults.go || true
        
	'';

        subPackages = [ "cmd/local-ai" ];
        doCheck = false;

        postInstall = ''
          [ -f $out/bin/local-ai ] && mv $out/bin/local-ai $out/bin/localai
        '';
      };
    };
}
