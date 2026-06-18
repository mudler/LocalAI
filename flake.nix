# Made by Azteczek
{
  description = "LocalAI flake";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      reactUi = pkgs.buildNpmPackage {
        pname = "localai-react-ui";
        version = "custom";
        src = ./core/http/react-ui;
        npmDepsHash = "sha256-G+bc1SajltRt4ZfkKNN1h6kFSmD0pCOR8MqRE6cKDLM=";
        npmBuildScript = "build";

        installPhase = ''
          runHook preInstall
          mkdir -p $out
          cp -r dist $out/
          runHook postInstall
        '';
      };
      localai-unwrapped = pkgs.buildGoModule {
        pname = "localai";
        version = "custom";

 	src = ./.;
        proxyVendor = true;
        vendorHash = "sha256-z3lxQS8mXFuJzvYamejwapwVEmLpeAoiO3ksUKb4I3Q=";

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

          mkdir -p core/http/react-ui
          cp -r ${reactUi}/dist core/http/react-ui/dist

          sed -i '/go:generate/d' core/config/inference_defaults.go || true

	'';

        subPackages = [ "cmd/local-ai" ];
        doCheck = false;

        postInstall = ''
          [ -f $out/bin/local-ai ] && mv $out/bin/local-ai $out/bin/localai
        '';
      };
    in {
      packages.${system} = {
        localai-unwrapped = localai-unwrapped;

        default = pkgs.buildFHSEnv {
          name = "localai";
          targetPkgs = pkgs: with pkgs; [
            localai-unwrapped
            bash
            coreutils
            gnugrep
          ];
          runScript = "${localai-unwrapped}/bin/localai";
        };
      };

      devShells.${system}.default = pkgs.mkShell {
        packages = with pkgs; [
          # Build toolchain (stdenv already provides gcc)
          go
          gnumake
          pkg-config
          cmake
          ccache
          protobuf
          go-protobuf
          protoc-gen-go
          protoc-gen-go-grpc

          # C++ gRPC + protobuf for the vendored llama.cpp backend
          # (backend/cpp/llama-cpp `make grpc-server`). The CMake build does
          # find_package(gRPC)/find_package(Protobuf); without grpc here the
          # shell exposes protobuf alone and the build fails to locate gRPC
          # (or links a stale, version-skewed grpc from the store). nixpkgs
          # builds `grpc` against this same `protobuf`, so the pair is
          # self-consistent. Docker (backend/Dockerfile.base-grpc-builder)
          # compiles gRPC v1.65.0 / protoc v27.1 from source; nixpkgs here is
          # newer (grpc 1.80 / protobuf 34) but wire- and ABI-consistent
          # within the backend. Pin protobuf_27 + a grpc override if exact
          # Docker version parity is ever required.
          grpc

          # Vulkan toolchain for the GGML Vulkan backends (e.g.
          # backend/cpp/privacy-filter BUILD_TYPE=vulkan, llama-cpp,
          # stablediffusion-ggml). ggml's find_package(Vulkan) needs the
          # headers + loader and shells out to glslc (from shaderc) to compile
          # shaders. Docker images install the LunarG SDK 1.4.335.0 instead
          # (backend/Dockerfile.{golang,python}); nixpkgs is newer but the
          # SPIR-V output is portable.
          vulkan-headers
          vulkan-loader
          vulkan-tools  # vulkaninfo, to sanity-check the ICD/driver
          shaderc       # glslc
          # ggml-vulkan #include <spirv/unified1/spirv.hpp>. nixpkgs splits the
          # header into its own output whose include dir the SPIRV-Headers CMake
          # target doesn't propagate, so a local vulkan build also needs
          # -DCMAKE_CXX_FLAGS=-I${pkgs.spirv-headers}/include. (The Docker SDK
          # install lands these in /usr/include, so it isn't needed there.)
          spirv-headers

          # React UI build (core/http/react-ui — `make react-ui`)
          nodejs
          bun  # alternative to npm, used by `make react-ui-docker`
          chromium  # Playwright e2e / UI coverage browser (see PLAYWRIGHT_CHROMIUM_PATH below)

          # Linting / static analysis (see `make lint`)
          golangci-lint
          gofumpt
          gotools  # goimports
          go-tools # staticcheck

          # Audio transforms: pkg/utils/ffmpeg_test.go shells out to the
          # `ffmpeg` CLI, exercised by `make test-coverage` (the pre-commit
          # gate). Headless build = the CLI without GUI/X deps.
          ffmpeg-headless

          # Common dev conveniences
          git
          curl
        ];

        shellHook = ''
          # Point Playwright at the nix-provided Chromium instead of its own
          # downloaded build, which can't resolve system libs (libglib-2.0, …)
          # on NixOS. playwright.config.js reads PLAYWRIGHT_CHROMIUM_PATH and
          # the Makefile skips `playwright install` when it's set.
          export PLAYWRIGHT_CHROMIUM_PATH="${pkgs.chromium}/bin/chromium"
          export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1

          echo "LocalAI dev shell: $(go version), node $(node --version)"
          echo "Build:       make build       (Go binary + React UI)"
          echo "React UI:    make react-ui    (npm install && vite build)"
          echo "Lint:        make lint        (only new issues vs master)"
          echo "           or make lint-all   (full baseline)"
        '';
      };
    };
}
