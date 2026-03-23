create the 1-reset.sh test script

1. cd $(dirname $0)/..

2. kills any process listening in port 8910 5173 4096

3. removes all the users from miniops
list them with `ops admin listuser `
and then delete each user with `ops admin deleteuser <user>`

4. remove everything from the folder ~/.ops-workspace

5. run air and keep in foregound
