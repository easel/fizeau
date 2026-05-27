#!/usr/bin/env bats
# Test harbor-runner image building and preflight sha-based rebuild logic

# Resolve absolute path to the benchmark directory
SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && cd .. && pwd)"
BUILD_SCRIPT="${SCRIPT_DIR}/build-harbor-runner.sh"
BENCHMARK_SCRIPT="${SCRIPT_DIR}/benchmark"
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

# Test 1: build-harbor-runner.sh produces image with correct content-sha label
@test "Test_BuildLabelMatchesContentSha" {
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

# Test 2: preflight rebuilds image only when content sha drifts
@test "Test_PreflightRebuildsOnShaChange" {
  # Compute the initial content SHA
  local initial_sha
  initial_sha=$(compute_content_sha)

  # Build the image once
  bash "${BUILD_SCRIPT}"

  # Get the initial image label
  local initial_label
  initial_label=$(docker image inspect "${IMAGE_TAG}" --format='{{index .Config.Labels "image-content-sha"}}')

  # Get the initial image ID
  local initial_image_id
  initial_image_id=$(docker image inspect "${IMAGE_TAG}" --format='{{.Id}}')

  # Run preflight with unchanged sources - should not rebuild
  bash "${BENCHMARK_SCRIPT}" preflight

  # Get the image label after preflight
  local after_preflight_label
  after_preflight_label=$(docker image inspect "${IMAGE_TAG}" --format='{{index .Config.Labels "image-content-sha"}}')

  # Get the image ID after preflight
  local after_preflight_image_id
  after_preflight_image_id=$(docker image inspect "${IMAGE_TAG}" --format='{{.Id}}')

  # Verify the label and ID are unchanged (no rebuild occurred)
  [ "$initial_label" = "$after_preflight_label" ]
  [ "$initial_image_id" = "$after_preflight_image_id" ]

  # Now mutate one of the adapter files
  local adapter_to_mutate="${ADAPTERS_DIR}/claude.py"
  echo "# test mutation comment" >> "$adapter_to_mutate"

  # Compute the new expected SHA
  local new_sha
  new_sha=$(compute_content_sha)

  # Verify the SHA actually changed
  [ "$initial_sha" != "$new_sha" ]

  # Build again - should produce new image with new label
  bash "${BUILD_SCRIPT}"

  # Get the new image label
  local new_label
  new_label=$(docker image inspect "${IMAGE_TAG}" --format='{{index .Config.Labels "image-content-sha"}}')

  # Get the new image ID
  local new_image_id
  new_image_id=$(docker image inspect "${IMAGE_TAG}" --format='{{.Id}}')

  # Verify the label and ID changed (rebuild occurred)
  [ "$initial_label" != "$new_label" ]
  [ "$new_sha" = "$new_label" ]
  [ "$initial_image_id" != "$new_image_id" ]

  # Run preflight again - should not rebuild since sha matches
  bash "${BUILD_SCRIPT}"
  local after_rebuild_label
  after_rebuild_label=$(docker image inspect "${IMAGE_TAG}" --format='{{index .Config.Labels "image-content-sha"}}')
  [ "$new_label" = "$after_rebuild_label" ]

  # Restore the mutated file
  git -C "${SCRIPT_DIR}" checkout "${adapter_to_mutate}"

  # Rebuild to restore original state
  bash "${BUILD_SCRIPT}"
}
