AutoRemesher core — vendored subset
===================================

Upstream:   https://github.com/huxingyi/autoremesher
Version:    1.0.0   (CHANGELOGS.md top entry; autoremesher.pro VERSION 1.0.0.9)
License:    MIT — the 1.0.0 release relicensed from GPLv3, see LICENSE
Imported:   2026-08-11
Commit:     <to be pinned — imported from a local source drop, not a clone>

Imported paths (upstream -> here):
    src/AutoRemesher/            -> src/AutoRemesher/
    include/AutoRemesher/        -> include/AutoRemesher/
    thirdparty/isotropicremesher -> isotropicremesher/
    LICENSE                      -> LICENSE

Deliberately NOT imported: the Qt application (src/*.cpp outside
src/AutoRemesher/), shaders, resources, and the bundled thirdparty/eigen,
thirdparty/tbb, thirdparty/QtAwesome, thirdparty/QtWaitingSpinner.

Eigen is consumed as an external dependency instead (vcpkg / find_package).
The bundled upstream copy is 5.0.1, which is the version this vendored core is
known to build against.

Local modifications: see PATCHES.md.
