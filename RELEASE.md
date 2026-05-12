# Release Process

Step-by-step checklist for cutting a new release of libvirt-volume-provisioner.

---

## 1. Decide the version number

Follows [Semantic Versioning](https://semver.org/):

| Change type | Version bump | Example |
|---|---|---|
| Breaking API or config change | MAJOR (X.0.0) | Removing a config field callers depend on |
| New feature, backwards-compatible | MINOR (X.Y.0) | New API endpoint, new config section |
| Bug fixes, dead-code removal, refactoring | PATCH (X.Y.Z) | Everything else |

---

## 2. Pre-release checklist

Complete every item before the release commit.

### Code quality
- [ ] `make test` passes locally with no failures
- [ ] `make lint` passes locally with 0 issues
- [ ] `go mod tidy` run — `go.mod` / `go.sum` are clean

### Version strings (update all three together)
- [ ] `cmd/provisioner/main.go` — `version = "vX.Y.Z"`
- [ ] `Makefile` — `DEB_VERSION ?= X.Y.Z`
- [ ] `CHANGELOG.md` — new section added (see §3 below)

### CHANGELOG entry quality (see §3 for guidance)
- [ ] Date is today in `YYYY-MM-DD` format
- [ ] Every user-visible change has an entry
- [ ] Breaking changes are clearly marked
- [ ] No placeholder or TODO text remains

---

## 3. Writing the CHANGELOG entry

Use the [Keep a Changelog](https://keepachangelog.com/en/1.0.0/) format.

```markdown
## [X.Y.Z] - YYYY-MM-DD

### Added
### Fixed
### Changed
### Removed
### Security
```

Only include sections that have content. Order: Added → Fixed → Changed → Removed → Security.

### What belongs in each section

**Added** — genuinely new capabilities a user or operator gains:
- New API endpoints or request fields
- New config options
- New CLI flags
- New Prometheus metrics or tracing spans
- New systemd units or packaging artefacts

**Fixed** — bugs where behaviour was wrong, missing, or unsafe:
- Crashes, panics, data races
- Incorrect calculations or logic errors
- Silent data loss or corruption
- Features that were wired up but never functioned

**Changed** — existing behaviour that works differently (not a bug, not new):
- Default value changes
- Dependency version bumps that affect runtime behaviour
- Performance characteristic changes
- Log format or field name changes
- Metric name or label changes

**Removed** — things callers or operators previously relied on that are now gone:
- Config fields (even ones that were silently ignored — operators may have set them)
- API fields or endpoints
- CLI flags
- Previously-supported env vars

**Security** — anything that closes a vulnerability or hardens an attack surface:
- Input validation fixes
- TLS/auth hardening
- Dependency CVE patches

### Writing style

- Lead each bullet with a **bold keyword** matching the symbol, method, or config field affected.
- One sentence explaining *what changed*. Second sentence (if needed) for *why* or *impact*.
- Avoid vague language: "various fixes", "improved X", "updated Y". Be specific.
- If a fix was invisible to operators but matters to developers, still include it — future
  contributors will search the changelog.

### What NOT to include

- Test-only changes with no behaviour impact
- Comment or documentation changes
- Internal refactors that don't change any observable behaviour
- Intermediate commits that were reverted before release

---

## 4. Release commit and tag

```bash
# Stage the three version files
git add cmd/provisioner/main.go Makefile CHANGELOG.md

# Commit
git commit -m "release: bump version to vX.Y.Z"

# Lightweight tag (not annotated — CI extracts version from tag name)
git tag vX.Y.Z

# Push branch and tag together
git push origin master
git push origin vX.Y.Z
```

---

## 5. Post-push verification

After pushing, confirm:

- [ ] GitHub Actions release workflow triggered (`.github/workflows/release.yml`)
- [ ] Tests and lint pass in CI
- [ ] Debian `.deb` package built with correct version (`libvirt-volume-provisioner_X.Y.Z_amd64.deb`)
- [ ] Docker image published to GHCR (`ghcr.io/rossigee/libvirt-volume-provisioner:vX.Y.Z`)
- [ ] GitHub Release created at https://github.com/rossigee/libvirt-volume-provisioner/releases
- [ ] Release assets include the `.deb` package
- [ ] Debian apt repository updated (B2-backed)

---

## 6. Required CI secrets

| Secret | Purpose |
|---|---|
| `GITHUB_TOKEN` | Auto-provided by GitHub |
| `B2_KEY_ID` | B2 bucket key for Debian repo upload |
| `B2_APPLICATION_KEY` | B2 bucket secret |
| `GPG_PRIVATE_KEY` | Signs the Debian repository |

---

## 7. Rollback

```bash
# Delete the tag locally and remotely
git tag -d vX.Y.Z
git push origin :refs/tags/vX.Y.Z

# Delete the GitHub release (leaves the tag gone)
gh release delete vX.Y.Z --yes

# Revert the release commit if needed
git revert HEAD
git push origin master
```

Remove the `.deb` from the B2 Debian repository manually if it was already published.

---

## 8. Manual release (CI fallback)

If the automated workflow fails:

```bash
# Build Debian package
make deb DEB_VERSION=X.Y.Z

# Build and push Docker image
make build-docker
docker tag libvirt-volume-provisioner:latest ghcr.io/rossigee/libvirt-volume-provisioner:vX.Y.Z
docker push ghcr.io/rossigee/libvirt-volume-provisioner:vX.Y.Z
docker tag libvirt-volume-provisioner:latest ghcr.io/rossigee/libvirt-volume-provisioner:latest
docker push ghcr.io/rossigee/libvirt-volume-provisioner:latest

# Create GitHub release with the .deb as an asset
gh release create vX.Y.Z libvirt-volume-provisioner_X.Y.Z_amd64.deb \
  --title "v X.Y.Z" \
  --notes-file <(sed -n '/^## \[X\.Y\.Z\]/,/^## \[/p' CHANGELOG.md | head -n -1)
```
