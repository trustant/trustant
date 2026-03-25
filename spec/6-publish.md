This file describes the publish APIs.
Put the code in the file `publish.go`

Three publish endpoints will be added:

- `POST /api/publish/push` — push code from `<workspacedir>/workspace/<name>` to the git remote
- `POST /api/publish/local` — deploy to the development environment using `.env`
- `POST /api/publish/remote` — deploy to production using `.env.production`

For now, all three are stubs. The frontend publish dropdown in `applist.html` shows "To be implemented" without calling any API.
