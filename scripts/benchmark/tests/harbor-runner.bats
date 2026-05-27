#!/usr/bin/env bats
# Acceptance tests for fizeau-harbor-runner Docker image build

# Resolve absolute path to the benchmark directory
SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && cd .. && pwd)"
BUILD_SCRIPT="${SCRIPT_DIR}/build-harbor-runner.sh"
DOCKERFILE="${SCRIPT_DIR}/Dockerfile.harbor-runner"
ADAPTERS_DIR="${SCRIPT_DIR}/harbor_adapters"
HARBOR_AGENT_PATH="${SCRIPT_DIR}/harbor_agent.py"
IMAGE_TAG="fizeau-harbor-runner:latest"

# Helper to compute content SHA the same way build-harbor-runner.sh does
compute_content_sha() {
  {
    LC_ALL=C find "${ADAPTERS_DIR}" -type f \
      | LC_ALL=C sort \
      | while IFS= read -r f; do
          printf '%s  %s\n' "$(sha256sum "$f" | awk '{print $1}')" "${f#"${SCRIPT_DIR}"/}"
        done
    printf '%s  %s\n' "$(sha256sum "${HARBOR_AGENT_PATH}" | awk '{print $1}')" "harbor_agent.py"
    printf '%s  %s\n' "$(sha256sum "${DOCKERFILE}" | awk '{print $1}')" "Dockerfile.harbor-runner"
  } | sha256sum | awk '{print $1}'
}

# Test 1: build-harbor-runner.sh produces fizeau-harbor-runner:latest and docker run --help works
@test "Test_harbor_runner_image_builds" {
  # Build the image
  bash "${BUILD_SCRIPT}"

  # Verify the image exists
  docker image inspect "${IMAGE_TAG}" >/dev/null

  # Verify docker run --help exits 0
  docker run --rm "${IMAGE_TAG}" --help
}

# Test 2: the built image's image-content-sha label equals computed content sha
@test "Test_image_label_matches_content_sha" {
  # Compute the expected content SHA
  local expected_sha
  expected_sha=$(compute_content_sha)

  # Build the image (will reuse cache if no changes)
  bash "${BUILD_SCRIPT}"

  # Get the actual image-content-sha label from the built image
  local actual_sha
  actual_sha=$(docker image inspect "${IMAGE_TAG}" --format='{{index .Config.Labels "image-content-sha"}}')

  # Verify they match
  [ "$expected_sha" = "$actual_sha" ]
}

# Test 3: mutating a harbor_adapter changes the content sha (and rebuild is triggered)
@test "Test_label_changes_when_adapter_changes" {
  # Get the initial content SHA
  local initial_sha
  initial_sha=$(compute_content_sha)

  # Build the image once
  bash "${BUILD_SCRIPT}"

  # Get the initial image label
  local initial_label
  initial_label=$(docker image inspect "${IMAGE_TAG}" --format='{{index .Config.Labels "image-content-sha"}}')

  # Verify initial state
  [ "$initial_sha" = "$initial_label" ]

  # Mutate one of the adapter files (add a comment at the end)
  local adapter_to_mutate="${ADAPTERS_DIR}/claude.py"
  echo "# test mutation" >> "$adapter_to_mutate"

  # Compute the new SHA (should be different)
  local new_sha
  new_sha=$(compute_content_sha)

  # Verify the SHA actually changed
  [ "$initial_sha" != "$new_sha" ]

  # Build again (should produce a new image with new label)
  bash "${BUILD_SCRIPT}"

  # Get the new image label
  local new_label
  new_label=$(docker image inspect "${IMAGE_TAG}" --format='{{index .Config.Labels "image-content-sha"}}')

  # Verify the label changed
  [ "$initial_label" != "$new_label" ]
  [ "$new_sha" = "$new_label" ]

  # Restore the mutated file to original state
  git -C "${SCRIPT_DIR}" checkout "${adapter_to_mutate}"

  # Rebuild again to restore original state
  bash "${BUILD_SCRIPT}"
}
