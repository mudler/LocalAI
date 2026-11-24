# Standalone mesh remeshing backend

The native library separates CPU geometry processing from TRELLIS inference:
CGAL Alpha Wrap, AutoRemesher quad-dominant topology, and material projection.
It loads no generation weights. Modes are wrap, quad, or wrap followed by quad.
Quad counts are density hints, not exact output counts. GLB triangulates quads;
the reported pre-triangulation quad count is not a claim of quad polygons in GLB.
Quad extraction can leave boundaries and must not be advertised as watertight.

`native/vendor` is an unmodified geometry-only snapshot from trellis2cpp commit
`f3f57f51395bf0a67e8211d90b13e4be72550644` in the reference checkout, not its uncommitted changes. It includes
AutoRemesher's documented Qt/TBB-free adaptation and the mesh export dependencies.
The original MIT licence and third-party notices are retained. `native/trellis2.h`
provides only the export macro: no TRELLIS generation library is linked or loaded.
Eigen is pinned separately in the Makefile.

CGAL Alpha Wrap carries GPL licensing obligations; separating the backend does
not remove them. Distribution must include the applicable licences and source
availability. The backend package must be labelled accordingly, rather than
advertising the combined native binary as MIT-only.

`make build` builds the native library, Go transport and installable `package/`.
Set `MESHREMESH_LIBRARY` to the absolute shared library path when running
`go test` to exercise real wrapping and material projection without a model.
Set `MESHREMESH_PACKAGE` to the absolute package directory to test its launcher,
gRPC model loading and all three remeshing modes in a separate process. These
tests do not require generation weights. The input currently must be a single,
untransformed, unskinned vertex-coloured GLB (such as the TRELLIS output); texture
atlas inputs are rejected rather than silently losing their materials.
The Eigen pin is maintained manually: the repository's
dependency bump bot currently only supports GitHub repositories, not GitLab.
