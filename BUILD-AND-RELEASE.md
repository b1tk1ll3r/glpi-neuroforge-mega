# Build & Release — GLPI NeuroForge Mega v1.6.2

The repository has one canonical image/release pipeline. Nested service workflows are intentionally not used.

## Project images

`docker buildx bake` builds all six immutable project images:

- `glpi-neuroforge-mega-neuroforge`
- `glpi-neuroforge-mega-neuroforge-worker`
- `glpi-neuroforge-mega-agent`
- `glpi-neuroforge-mega-agent-data-init`
- `glpi-neuroforge-mega-knowledge`
- `glpi-neuroforge-mega-control`

Default registry/tag:

```text
git.send.nrw/sendnrw/<image>:1.6.2
```

Override without editing the bake file:

```bash
IMAGE_TAG=1.6.2 REGISTRY=git.send.nrw/sendnrw docker buildx bake
```

## Gitea Actions

`.gitea/workflows/ci.yml` runs test/vet/build for all four Go modules, static release checks and Docker builds.

`.gitea/workflows/release.yml` runs only on immutable `v*` tags. The tag must exactly match the root `VERSION` file. It pushes all six project images with the version tag only; no production dependency on `latest` is introduced.

Required registry secrets:

```text
DOCKER_USERNAME
DOCKER_PASSWORD
```

Release example:

```bash
git tag v1.6.2
git push origin v1.6.2
```

## Local source gate

```bash
./scripts/release-gate.sh
```

This executes static production checks, Compose environment isolation, secret scanning, graph reproducibility, test/vet/build for every module, and targeted race checks.
