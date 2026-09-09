# Build and Release

## Repository image set

The repository publishes six immutable project images:

- `glpi-neuroforge-mega-neuroforge`
- `glpi-neuroforge-mega-neuroforge-worker`
- `glpi-neuroforge-mega-agent`
- `glpi-neuroforge-mega-agent-data-init`
- `glpi-neuroforge-mega-knowledge`
- `glpi-neuroforge-mega-control`

The default registry is `git.send.nrw/sendnrw`.

## Local build

From repository root:

```bash
docker buildx bake
```

Override registry/tag when required:

```bash
REGISTRY=registry.example/org IMAGE_TAG=1.6.2 docker buildx bake
```

## CI

`.gitea/workflows/ci.yml` runs `go test ./...` and `go vet ./...` for NeuroForge, Agent, Knowledge and Control and then performs a Buildx Bake build of the full image set.

## Release

Set `VERSION` and all compose/deployment tags to the desired immutable version, then push a matching tag:

```bash
git tag v1.6.2
git push origin v1.6.2
```

`.gitea/workflows/release.yml` verifies that the git tag matches `VERSION` and pushes all six images with tag `1.6.2`. Project production compose files do not require `latest`.
