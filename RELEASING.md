# Release Process

This document describes how to create a new release of Bifrost.

## Prerequisites

- Write access to the repository
- All changes committed and pushed to `main` branch
- All tests passing on CI
- CHANGELOG.md updated with the new version

## Release Steps

### 1. Update CHANGELOG.md

Before creating a release, update the CHANGELOG.md file:

```markdown
## [0.2.0] - 2026-01-15

### Added
- New feature X
- Enhancement Y

### Fixed
- Bug fix Z

### Changed
- Breaking change description
```

Move any items from `[Unreleased]` to the new version section.

Commit the changelog:

```bash
git add CHANGELOG.md
git commit -m "chore: update changelog for v0.2.0"
git push origin main
```

### 2. Create and Push a Version Tag

Create a semantic version tag (must start with `v`):

```bash
# For a regular release
git tag -a v0.2.0 -m "Release v0.2.0"

# Or for a prerelease
git tag -a v0.2.0-beta.1 -m "Release v0.2.0-beta.1"

# Push the tag to trigger the release workflow
git push origin v0.2.0
```

### 3. Automated Release Process

Once the tag is pushed, GitHub Actions will automatically:

1. **Run Tests** (5-10 minutes)
   - Execute all tests with race detection
   - Generate coverage report
   - Upload `coverage.txt` as a GitHub Actions artifact
   - Write a one-line coverage summary in the job summary

2. **Build Binaries** (10-15 minutes)
   - Build for Linux (amd64, arm64)
   - Build for macOS (amd64, arm64)
   - Generate SHA256 checksums for each binary
   - Total: 4 binaries + 4 checksums = 8 artifacts

3. **Build and Push Docker Images** (5-10 minutes)
   - Build multi-platform Docker images (linux/amd64, linux/arm64)
   - Push to GitHub Container Registry (ghcr.io)
   - Tag with semantic versions (e.g., v0.2.0, 0.2, 0, latest)
   - Include version information in the image

4. **Create GitHub Release** (1-2 minutes)
   - Extract changelog for the version
   - Create release with all binaries attached
   - Mark as prerelease if tag contains `alpha`, `beta`, or `rc`

### 4. Verify the Release

After the workflow completes (15-30 minutes total):

1. **Check GitHub Release**:
   - Visit https://github.com/pebo/bifrost/releases
   - Verify the release appears with correct version
   - Verify all binaries are attached (4 binaries + 4 checksums = 8 files)
   - Verify changelog content is displayed

2. **Verify Docker Images**:
   ```bash
   # Pull the image
   docker pull ghcr.io/pebo/bifrost:0.2.0
   
   # Check available tags
   docker images ghcr.io/pebo/bifrost
   
   # Test the image
   docker run --rm ghcr.io/pebo/bifrost:0.2.0 -version
   
   # Run with a config file
   docker run --rm -v $(pwd)/example-config.yaml:/etc/bifrost/config.yaml \
     ghcr.io/pebo/bifrost:0.2.0 -config /etc/bifrost/config.yaml
   ```
   
   - Visit https://github.com/pebo/bifrost/pkgs/container/bifrost
   - Verify the new version tags are visible
   - Check that both linux/amd64 and linux/arm64 platforms are available

3. **Verify pkg.go.dev**:
   - Visit https://pkg.go.dev/github.com/pebo/bifrost
   - The new version should appear (may take 10-15 minutes)
   - Check that documentation is up to date

4. **Test a Binary**:
   ```bash
   # Download a binary
   wget https://github.com/pebo/bifrost/releases/download/v0.2.0/bifrost-v0.2.0-linux-amd64
   
   # Verify checksum
   wget https://github.com/pebo/bifrost/releases/download/v0.2.0/bifrost-v0.2.0-linux-amd64.sha256
   sha256sum -c bifrost-v0.2.0-linux-amd64.sha256
   
   # Run the binary
   chmod +x bifrost-v0.2.0-linux-amd64
   ./bifrost-v0.2.0-linux-amd64 -version
   ```

## Version Numbering

Follow [Semantic Versioning](https://semver.org/):

- **MAJOR** version (v1.0.0 → v2.0.0): Incompatible API changes
- **MINOR** version (v0.1.0 → v0.2.0): New functionality, backwards compatible
- **PATCH** version (v0.1.0 → v0.1.1): Backwards compatible bug fixes

### Prerelease Versions

For prereleases, append a suffix:

- **Alpha**: `v0.2.0-alpha.1` - Early testing, unstable
- **Beta**: `v0.2.0-beta.1` - Feature complete, needs testing
- **Release Candidate**: `v0.2.0-rc.1` - Final testing before release

## Troubleshooting

### Release Workflow Failed

If the GitHub Actions workflow fails:

1. Check the workflow logs at https://github.com/pebo/bifrost/actions
2. Fix the issue in code
3. Delete the failed tag:
   ```bash
   git tag -d v0.2.0
   git push origin :refs/tags/v0.2.0
   ```
4. Create a new tag after fixes

### Wrong Version Tagged

If you tagged the wrong commit:

1. Delete the local tag:
   ```bash
   git tag -d v0.2.0
   ```

2. Delete the remote tag:
   ```bash
   git push origin :refs/tags/v0.2.0
   ```

3. Delete the GitHub release (if created)
   - Go to https://github.com/pebo/bifrost/releases
   - Click the release, then "Delete"

4. Create the correct tag:
   ```bash
   git tag -a v0.2.0 -m "Release v0.2.0"
   git push origin v0.2.0
   ```

### Missing Binaries in Release

If some binaries are missing from the release:

1. Check if the build job failed for specific platforms
2. Re-run the failed jobs from the Actions tab
3. Or delete and recreate the tag to trigger a fresh build

## Post-Release Tasks

After a successful release:

1. **Announce the Release**:
   - Update documentation site (if applicable)
   - Post on social media or forums
   - Notify users via mailing list

2. **Update Dependencies**:
   - If this is a library, notify dependent projects
   - Update examples to use the new version

3. **Monitor Issues**:
   - Watch for bug reports related to the new release
   - Be ready to create a patch release if needed

## Quick Reference

```bash
# Complete release flow
git checkout main
git pull origin main

# Update CHANGELOG.md
vim CHANGELOG.md
git add CHANGELOG.md
git commit -m "chore: update changelog for v0.2.0"
git push origin main

# Create and push tag
git tag -a v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0

# Wait for GitHub Actions to complete (~20 minutes)
# Verify at https://github.com/pebo/bifrost/releases
```

## Emergency Release

For critical bug fixes:

1. Create a hotfix branch from the release tag:
   ```bash
   git checkout -b hotfix-v0.1.1 v0.1.0
   ```

2. Fix the bug and commit:
   ```bash
   git commit -m "fix: critical security issue"
   ```

3. Update CHANGELOG.md with patch notes

4. Tag and push:
   ```bash
   git tag -a v0.1.1 -m "Release v0.1.1"
   git push origin v0.1.1
   ```

5. Merge back to main:
   ```bash
   git checkout main
   git merge hotfix-v0.1.1
   git push origin main
   ```
