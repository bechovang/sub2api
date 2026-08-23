.PHONY: build build-backend test test-backend

# Strip: backend-only build (Key-Auth Gateway + Admin API). Frontend removed.
build: build-backend

# 编译后端（复用 backend/Makefile）
build-backend:
	@$(MAKE) -C backend build

# 运行测试（后端）
test: test-backend

test-backend:
	@$(MAKE) -C backend test
