@echo off
set PATH=C:\Program Files\Go\bin;%PATH%
cd /d d:\go\shm-go

echo ===== 1. ALL TESTS =====
go test ./pkg/shmcache/ -v -count=1
if %ERRORLEVEL% neq 0 (
    echo FAILED - stopping
    pause
    exit /b %ERRORLEVEL%
)

echo ===== 2. COVERAGE =====
go test ./pkg/shmcache/ -coverprofile=coverage.out -covermode=atomic -count=1
go tool cover -func=coverage.out | findstr total

echo ===== 3. BENCHMARKS =====
go test ./pkg/shmcache/ -bench=. -benchmem -count=1 -timeout 300s

echo ===== 4. GIT PUSH =====
git init
git add -A
git commit -m "Initial commit: shared memory cache service (shm-go)"
git remote add origin https://github.com/hengli-coder/shm-go.git
git branch -M main
git push -u origin main

pause
