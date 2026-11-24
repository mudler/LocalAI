// The single translation unit that instantiates tinybvh.
//
// tiny_bvh.h is header-only with an explicit implementation switch; defining
// TINYBVH_IMPLEMENTATION in more than one TU produces duplicate symbols. Keeping
// it here means every consumer (print_remesh.cpp today, an ambient-occlusion or
// raycast bake later) is a plain #include away. See third_party/tinybvh/VERSION.md.

#define TINYBVH_IMPLEMENTATION
#include "tiny_bvh.h"
