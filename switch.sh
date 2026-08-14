#!/bin/bash
BRANCH=${1:?branch} 
set -e
git checkout -b "$BRANCH" 
git submodule update --init --recursive
