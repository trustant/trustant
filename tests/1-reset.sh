#!/bin/bash
# 1-reset.sh - Reset test environment

# 1. cd to project root
cd "$(dirname "$0")/.."

# 2. Kill any process listening on ports 8910, 5173, 4096
for port in 8910 5173 4096; do
    pid=$(lsof -ti :$port 2>/dev/null)
    if [ -n "$pid" ]; then
        echo "Killing process on port $port (PID: $pid)"
        kill -9 $pid 2>/dev/null
    fi
done

# 3. Remove all users from miniops
echo "Removing all users..."
users=$(ops admin listuser 2>/dev/null | awk 'NR>1{print $1}')
for user in $users; do
    if [ -n "$user" ]; then
        echo "Deleting user: $user"
        ops admin deleteuser "$user"
    fi
done

# 4. Remove everything from ~/.ops-workspace
echo "Cleaning ~/.ops-workspace..."
rm -rf ~/.ops-workspace/*

# 5. Start air in foreground
echo "Starting air..."
air
