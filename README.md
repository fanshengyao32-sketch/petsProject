# petsProject

题目 02“宠物门店的预约、接待、服务与取消退款”的 Go 教学项目。

## 技术栈

- Go、Gin、GORM
- MySQL 8.0 / InnoDB
- `html/template`
- 独立模拟支付 HTTP 服务

## 本地启动

```bash
docker compose up --build
```

启动后访问：

- 主服务：<http://localhost:8080>
- 存活检查：<http://localhost:8080/health/live>
- 就绪检查：<http://localhost:8080/health/ready>
- 模拟支付：<http://localhost:8081/health/live>
- MySQL：`localhost:3307`（容器内仍为 `3306`）

当前只完成工程骨架，具体预约业务将在后续任务中逐步实现。原型和过程材料位于 `项目文档/`。
