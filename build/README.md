# Images

| Image               | Build                                                                 |
|---------------------|-----------------------------------------------------------------------|
| `network-controller`| `docker build -f build/Dockerfile -t network-controller .`            |
| `discovery`         | `docker build -f build/Dockerfile.discovery -t discovery .`           |
| `ephemeral`         | `docker build -f build/Dockerfile.ephemeral --build-arg BASE=... -t ephemeral .` (needs the MP-SPDZ runtime base, see the file header) |

CI publishes all three with `ko` (`.github/workflows/service.publish.yaml`, `.ko.yaml`), which uses the private
MP-SPDZ base image for `ephemeral`. The Dockerfiles above are the reproducible alternative used for local and kind
testing.
