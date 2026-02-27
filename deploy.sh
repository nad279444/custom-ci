#!/bin/bash

# Log everything
exec >> /var/log/deploy.log 2>&1
echo "=============================="
echo "Deploy started: $(date)"
echo "=============================="

cd /home/nad/projects/custom-ci

echo "Pulling latest code..."
git pull origin main

echo "Rebuilding and restarting CI services..."
docker compose up -d --build

echo "Services status:"
docker compose ps

echo "Deploy finished: $(date)"
echo ""
