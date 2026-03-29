# Trustable App Development Guide

This is a serverless application with a TypeScript/React frontend and a Python backend.

## Architecture

- **Frontend**: TypeScript with React and Tailwind CSS, sources under `src/`
- **Backend**: Python serverless functions ("actions"), sources under `packages/<package>/<action>/`
- Functions and actions are synonyms; each produces an API endpoint

## API Endpoints

- Backend APIs are available at `/api/my/<package>/<action>` (package is usually `v1`)
- To access an action with streaming output, given `<proto>://<user>.<domain>`, POST to `<proto>://stream.<domain>/web/<package>/<action>` (returns a stream of JSON objects)

## Dependencies

- **Frontend**: add to `package.json` and run `npm install`
- **Backend**: add to `packages/<package>/<action>/requirements.txt` (per-action, do not rebuild)

## Important Rules

- Never try to build or deploy; this is managed automatically when you edit sources
- Never create a backend server; create new API endpoints using actions
- Never deploy actions manually; deployment happens automatically
