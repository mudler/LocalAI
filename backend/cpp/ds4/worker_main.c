// ds4-worker: standalone distributed worker for the LocalAI ds4 backend.
//
// A ds4 distributed worker owns a slice of the model's transformer layers,
// dials the coordinator, and serves activations for its slice. It does NOT
// speak backend.proto - it speaks ds4's own TCP transport via ds4_dist_run().
// This binary is intentionally minimal (no HTTP/web/kvstore/linenoise): it
// only needs the engine objects + ds4_distributed.o, which the backend already
// builds. It is launched by `local-ai worker ds4-distributed`.
//
// Usage:
//   ds4-worker --role worker --model <gguf> --layers 20:output \
//              --coordinator <host> <port> [--cpu|--cuda|--metal] [-c CTX] [-t N]

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <signal.h>
#include <limits.h>

#include "ds4.h"
#include "ds4_distributed.h"

static const char *need_arg(int *i, int argc, char **argv, const char *flag) {
    if (*i + 1 >= argc) {
        fprintf(stderr, "ds4-worker: missing value for %s\n", flag);
        exit(2);
    }
    return argv[++(*i)];
}

static int parse_int_arg(const char *s, const char *flag) {
    char *end = NULL;
    long v = strtol(s, &end, 10);
    if (!s[0] || *end || v <= 0 || v > INT_MAX) {
        fprintf(stderr, "ds4-worker: invalid value for %s: %s\n", flag, s);
        exit(2);
    }
    return (int)v;
}

static ds4_backend default_backend(void) {
#if defined(DS4_NO_GPU)
    return DS4_BACKEND_CPU;
#elif defined(__APPLE__)
    return DS4_BACKEND_METAL;
#else
    return DS4_BACKEND_CUDA;
#endif
}

int main(int argc, char **argv) {
    signal(SIGPIPE, SIG_IGN);

    ds4_engine_options opt = {0};
    opt.backend = default_backend();
    int ctx_size = 32768;

    for (int i = 1; i < argc; i++) {
        const char *arg = argv[i];
        if (!strcmp(arg, "-h") || !strcmp(arg, "--help")) {
            fprintf(stdout, "ds4-worker: standalone ds4 distributed worker\n");
            ds4_dist_usage(stdout);
            fprintf(stdout, "  -m, --model PATH   model GGUF (the worker loads only its --layers slice)\n");
            fprintf(stdout, "  -c, --ctx N        context size (default 32768)\n");
            fprintf(stdout, "  -t, --threads N    CPU threads\n");
            fprintf(stdout, "  --cpu|--cuda|--metal  backend override\n");
            return 0;
        }

        char dist_err[256] = {0};
        ds4_dist_cli_parse_result dist_parse =
            ds4_dist_parse_cli_arg(arg, &i, argc, argv, &opt.distributed,
                                   dist_err, sizeof(dist_err));
        if (dist_parse == DS4_DIST_CLI_ERROR) {
            fprintf(stderr, "ds4-worker: %s\n",
                    dist_err[0] ? dist_err : "invalid distributed option");
            return 2;
        }
        if (dist_parse == DS4_DIST_CLI_MATCHED) continue;

        if (!strcmp(arg, "-m") || !strcmp(arg, "--model")) {
            opt.model_path = need_arg(&i, argc, argv, arg);
        } else if (!strcmp(arg, "-c") || !strcmp(arg, "--ctx")) {
            ctx_size = parse_int_arg(need_arg(&i, argc, argv, arg), arg);
        } else if (!strcmp(arg, "-t") || !strcmp(arg, "--threads")) {
            opt.n_threads = parse_int_arg(need_arg(&i, argc, argv, arg), arg);
        } else if (!strcmp(arg, "--cpu")) {
            opt.backend = DS4_BACKEND_CPU;
        } else if (!strcmp(arg, "--cuda")) {
            opt.backend = DS4_BACKEND_CUDA;
        } else if (!strcmp(arg, "--metal")) {
            opt.backend = DS4_BACKEND_METAL;
        } else {
            fprintf(stderr, "ds4-worker: unknown option: %s\n", arg);
            return 2;
        }
    }

    if (opt.distributed.role != DS4_DISTRIBUTED_WORKER) {
        fprintf(stderr, "ds4-worker: --role worker is required\n");
        return 2;
    }
    if (!opt.model_path) {
        fprintf(stderr, "ds4-worker: --model is required\n");
        return 2;
    }

    char prep_err[256] = {0};
    if (ds4_dist_prepare_engine_options(&opt.distributed, &opt,
                                        prep_err, sizeof(prep_err)) != 0) {
        fprintf(stderr, "ds4-worker: %s\n", prep_err);
        return 2;
    }

    ds4_engine *engine = NULL;
    if (ds4_engine_open(&engine, &opt) != 0 || !engine) {
        fprintf(stderr, "ds4-worker: failed to open engine\n");
        return 1;
    }

    ds4_dist_generation_options gen = {0};
    gen.ctx_size = ctx_size;
    int rc = ds4_dist_run(engine, &opt.distributed, &gen);
    ds4_engine_close(engine);
    return rc;
}
