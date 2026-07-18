#!/bin/bash
cd "d:/go/shm-go"
git init
git add -A
git commit -m "Initial commit: shared memory cache service"
git remote add origin https://github.com/hengli-coder/shm-go.git
git branch -M main
git push -u origin main
