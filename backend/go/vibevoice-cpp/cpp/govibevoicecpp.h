#pragma once

// Re-exports the vibevoice.cpp flat C ABI so this MODULE library
// resolves the same symbols that purego.RegisterLibFunc looks up by
// name. The actual definitions live in libvibevoice (linked PRIVATE).

#include "vibevoice_capi.h"
