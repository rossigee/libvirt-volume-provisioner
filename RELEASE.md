# Release Process

This document outlines the step-by-step process for releasing new versions of libvirt-volume-provisioner.

## Pre-Release Checklist

Before creating a release, ensure all these items are completed:

- [ ] **Update Version**: Update `DEB_VERSION` in Makefile and version in `cmd/provisioner/main.go`
- [ ] **Update CHANGELOG.md**: Add new section with version, date, and changes
- [ ] **Update README.md**: Update version references if needed
- [ ] **Run Tests**: Execute `make test` locally
- [ ] **Run Linting**: Execute `make lint` locally
- [ ] **Build Locally**: Test `make build` and `make deb` work
- [ ] **Commit Changes**: All version/changelog changes committed and pushed
- [ ] **Branch Status**: Working on clean `master` branch

## Release Steps

### 1. Create Release Tag

```bash
# Ensure you're on master and up-to-date
git checkout master
git pull origin master

# Create annotated tag (replace X.Y.Z with actual version)
git tag -a vX.Y.Z -m "Release vX.Y.Z"

# Push the tag to trigger release workflow
git push origin vX.Y.Z
```

### 2. Monitor Release Pipeline

The GitHub Actions release workflow will automatically:

1. **Extract Version**: Parse version from tag
2. **Run Tests**: Execute full test suite
3. **Run Linting**: Code quality checks
4. **Build Debian Package**: Create `.deb` with correct version
5. **Build Docker Images**: Push to GHCR with version tags
6. **Update Debian Repository**: Upload to B2-backed repo
7. **Create GitHub Release**: Generate release with changelog

### 3. Post-Release Verification

After the workflow completes, verify:

- [ ] **GitHub Release**: Created at https://github.com/rossigee/libvirt-volume-provisioner/releases
- [ ] **Release Assets**: `.deb` package uploaded with correct version
- [ ] **Docker Images**: Available at `ghcr.io/rossigee/libvirt-volume-provisioner:vX.Y.Z`
- [ ] **Debian Repository**: Package available via `apt install libvirt-volume-provisioner`
- [ ] **Changelog**: Auto-generated changelog includes recent commits

## Automated Workflow Details

### Trigger Condition
- **Event**: Push to `tags` matching `v*` pattern
- **Workflow File**: `.github/workflows/release.yml`

### Environment Variables
- `REGISTRY`: `ghcr.io`
- `IMAGE_NAME`: `rossigee/libvirt-volume-provisioner`

### Required Secrets
- `GITHUB_TOKEN`: Auto-provided by GitHub
- `B2_KEY_ID`: B2 application key ID
- `B2_APPLICATION_KEY`: B2 application key
- `GPG_PRIVATE_KEY`: GPG private key for repository signing

## Troubleshooting

### Common Issues

**Version Mismatch in Package Name**
- **Cause**: Makefile `DEB_VERSION` not updated or workflow not passing version
- **Fix**: Ensure `DEB_VERSION ?= X.Y.Z` in Makefile and workflow passes `DEB_VERSION=${{ steps.version.outputs.version }}`

**Tests Failing in CI**
- **Cause**: Tests pass locally but fail in GitHub Actions
- **Fix**: Check for environment differences, missing dependencies, or timing issues

**Docker Build Failing**
- **Cause**: Multi-stage build issues or missing dependencies
- **Fix**: Test locally with `make build-docker`

**Repository Upload Failing**
- **Cause**: B2 credentials expired or bucket permissions
- **Fix**: Update GitHub secrets with fresh B2 credentials

### Manual Release (Fallback)

If automated release fails, perform manually:

```bash
# Build Debian package
make deb DEB_VERSION=X.Y.Z

# Build and push Docker image
make build-docker
docker tag libvirt-volume-provisioner:latest ghcr.io/rossigee/libvirt-volume-provisioner:vX.Y.Z
docker push ghcr.io/rossigee/libvirt-volume-provisioner:vX.Y.Z

# Create GitHub release manually
gh release create vX.Y.Z libvirt-volume-provisioner_X.Y.Z_amd64.deb \
  --title "Release vX.Y.Z" \
  --generate-notes
```

## Rollback Procedures

If a release needs to be rolled back:

1. **Delete Git Tag**: `git tag -d vX.Y.Z && git push origin :refs/tags/vX.Y.Z`
2. **Delete GitHub Release**: `gh release delete vX.Y.Z`
3. **Delete Docker Images**: `docker rmi ghcr.io/rossigee/libvirt-volume-provisioner:vX.Y.Z`
4. **Remove from Debian Repo**: Manual removal from B2 bucket (contact maintainers)

## Version Numbering

Follows [Semantic Versioning](https://semver.org/):

- **MAJOR**: Breaking changes (X.0.0)
- **MINOR**: New features, backward compatible (X.Y.0)
- **PATCH**: Bug fixes, backward compatible (X.Y.Z)

## Release Cadence

- **Patch Releases**: As needed for critical bug fixes
- **Minor Releases**: Monthly or when significant features complete
- **Major Releases**: When breaking changes are necessary

## Contact

For release issues or questions:
- Check GitHub Actions logs for workflow failures
- Review deployment.md for detailed deployment procedures
- Contact maintainers for repository or secret issues