// SPDX-License-Identifier: MIT
#pragma once
// Only the logging/backend declarations used by the production wrapper.
enum ggml_log_level { GGML_LOG_LEVEL_DEBUG, GGML_LOG_LEVEL_INFO,
                      GGML_LOG_LEVEL_WARN, GGML_LOG_LEVEL_ERROR };
void ggml_log_set(void (*)(ggml_log_level, const char *, void *), void *);
void ggml_backend_load_all();
