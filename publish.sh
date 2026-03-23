#!/bin/bash
git tag -d $(git tag)
git tag $(date +%Y.%m%d.%H%S)
TAG=$(git tag)
git commit -m "$TAG" -a
git push origin main --tags
