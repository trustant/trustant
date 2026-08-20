#!/bin/bash
# Publish: push tag, watch CI build, then push olaris-bestia
set -euo pipefail

# Ensure gh is authenticated
if ! gh auth status >/dev/null 2>&1; then
    echo "Error: not logged in to GitHub. Run 'gh auth login' first."
    exit 1
fi

# Get the most recent tag
TAG=$(git tag --sort=-creatordate | head -1)
if [ -z "$TAG" ]; then
    echo "Error: no tags found."
    exit 1
fi
# CI routes on the tag shape, so publish reports which build the operator is
# about to wait on: a hotfix layer is a couple of minutes, a full image ~20.
HOTFIX=false
if [[ "$TAG" =~ -[0-9]+$ ]]; then
    HOTFIX=true
    echo "Publishing hotfix: $TAG"
else
    echo "Publishing image: $TAG"
fi

# Push the current HEAD to the selected release branch plus tags.
CURRENT_BRANCH=$(git branch --show-current)
TARGET_BRANCH="${TRUSTABLE_PUBLISH_BRANCH:-$CURRENT_BRANCH}"
echo "Pushing HEAD to origin/$TARGET_BRANCH plus tags..."
git push origin "HEAD:$TARGET_BRANCH" --tags

# Wait for a workflow run matching this tag to appear
echo "Waiting for CI run for tag $TAG..."
REPO="trustable-ai/trustable-app"
for i in $(seq 1 30); do
    RUN_ID=$(gh run list --repo "$REPO" --limit 5 --json databaseId,headBranch,status \
        --jq ".[] | select(.headBranch==\"$TAG\") | .databaseId" | head -1)
    if [ -n "$RUN_ID" ]; then
        break
    fi
    sleep 2
done

if [ -z "$RUN_ID" ]; then
    echo "Error: no CI run found for tag $TAG after 60s."
    exit 1
fi

echo "Found run $RUN_ID, watching..."
gh run watch "$RUN_ID" --repo "$REPO"

# Check if the run succeeded
STATUS=$(gh run view "$RUN_ID" --repo "$REPO" --json conclusion --jq '.conclusion')
if [ "$STATUS" != "success" ]; then
    echo "Error: CI run failed with status: $STATUS"
    exit 1
fi

# A hotfix must not touch olaris-bestia at all -- not the cd, not the commit,
# not the push. hotfix.sh never writes opsroot.json, so `git commit -a` there
# could only sweep up UNRELATED dirty files under a message naming the hotfix
# tag, and the push is an unauthorized olaris* push.
if $HOTFIX; then
    echo "CI passed. Hotfix image $TAG is in the registry."
    echo
    echo "opsroot.json still points at the base image: a hotfix patches the"
    echo "running StatefulSet and is not a release. To promote it, run"
    echo "./build.sh --build and push olaris-bestia with explicit authorization."
    echo "Done. Published $TAG successfully."
    exit 0
fi

echo "CI passed. Pushing olaris-bestia..."
cd olaris-bestia
if git diff --quiet && git diff --cached --quiet; then
    echo "No olaris-bestia changes to commit."
else
    git commit -m "$TAG" -a
fi
git push origin main --tags

echo "Done. Published $TAG successfully."
