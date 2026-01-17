This file describe the code for the application page, put the code in `app.html`

# Application

This page shows the current application
reading it the cookie LEFT, RIGHT and NAME

It shows a full page, with a top bar with 5% high.

In the bar there is

- the trusable logo
- the application name
- the button (alighed to right) to go back

In the body there are two iframes, 50% width and 95% heigh (full page except for the topo bar)

The will show in the body th

`<current-site>:<left-port>` and <current-site>:<right-port>` in the body

Clicking on the button back will invoke the DELETE /api/app to stop running subprocess