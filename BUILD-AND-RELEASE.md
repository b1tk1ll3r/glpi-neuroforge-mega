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

Additional tags and the OCI source label can be set with `EXTRA_TAGS` (comma-separated) and `SOURCE_URL`.

## CI (GitHub Actions)

`.github/workflows/ci.yml` runs on pushes and pull requests to `main`:

- `go vet`, `go test` and `go build` for NeuroForge, Agent, Knowledge and Control
- the static release gates (`preflight.sh --static`, `check-compose-env.py`, `secret-scan.sh`, `make engineering-graph-check`, `verify-package.sh`) and the targeted race checks from `scripts/release-gate.sh`
- a Buildx Bake build of the full image set

On pushes to `main` the image set is additionally pushed with the tag `git describe --tags` (without `v`) and `latest`.

### Registry

Without configuration the workflows push to GHCR (`ghcr.io/<owner>`) with the built-in `GITHUB_TOKEN`. To push to a different registry (e.g. `git.send.nrw/sendnrw`, which the production compose files reference), set the repository variable `REGISTRY` and the secrets `DOCKER_USERNAME` / `DOCKER_PASSWORD`. The registry must be reachable via HTTPS from the runner.

### Dependabot

`.github/dependabot.yml` checks GitHub Actions, the Dockerfile base images of all four modules and Go modules weekly, grouped into one pull request per ecosystem.

## Release

Set `VERSION` and all compose/deployment tags to the desired immutable version, then push a matching tag:

```bash
git tag v1.6.2
git push origin v1.6.2
```

`.github/workflows/release.yml` first runs the complete CI (without the duplicate image build), verifies that the git tag matches `VERSION`, pushes all six images with tag `1.6.2` and creates a GitHub release (notes from `RELEASE-NOTES-v1.6.2.md` when present). Project production compose files do not require `latest`.
