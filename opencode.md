# Trustable App Development Guide

This is a serverless application with a TypeScript/React frontend and a Python backend.

## Important Rules

- Never try to build or deploy; this is managed automatically when you edit sources
- Never create a backend server; create new API endpoints using actions
- Never create `__main__.py` files; always use the `new-api-endpoint` tool

## Architecture

- **Frontend**: TypeScript with React and Tailwind CSS, sources under `src/`
- **Backend**: Python serverless functions ("actions"), sources are under `packages/<package>/<action>/`
- Functions and actions are synonyms; each produces an API endpoint available in the sames server as `/api/my/<package>/<action>`

## API Endpoints

- Backend APIs are available at `/api/my/<package>/<action>` (package is usually `v1`)
- To access an action with streaming output, given `<proto>://<user>.<domain>`, POST to `<proto>://stream.<domain>/web/<package>/<action>` (returns a stream of JSON objects)

## Dependencies

### Frontend
-  add frontend dependencies to `package.json` then run `npm install`

### Backend

- When you need a SQL database, **ALWAYS** use the tool `add-postgresql` and the provided context and the already provided `psicopg` library to access it. Never try to add requirements or use directly database connections.

- When you need redis, **ALWAYS** use the tool `add-redis` and the provided context and the already provided `redis` library. Never try to add requirements or use directly redis connections.

- When you need s3, **ALWAYS** use the tool `add-s3` and the provided context and the already provided `boto3` library. Never try to add requirements or use directly s3 connections.

- When you need s3, **ALWAYS** use the tool `add-s3` and the provided context and the already provided `boto3` library. Never try to add requirements or use directly s3 connections.

- When you need a vector database, **ALWAYS** use the tool `add-milvus` and the provided context and the already provided `pymilvus` library. Never try to add requirements or use directly milvus connections.

- Always use the `ensure-requirements` tool to add Python libraries. Never edit `requirements.txt` directly.

## Creating Backend Actions

- **ALWAYS** use the `new-api-endpoint` tool to create new API endpoints. Never create `__main__.py` or action directories directly

- Never edit the `__main__.py`, edit the `packages/<package>/<action>/<action>.py`. For each API invocation the main function of this module is invoked with the parameters and the context to access services.


