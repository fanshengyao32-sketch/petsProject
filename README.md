# petsProject

题目 02“宠物门店的预约、接待、服务与取消退款”的 Go 教学项目。

## 当前状态

- R-01～R-12、S-01～S-05：独立验收通过；
- PR [#1](https://github.com/fanshengyao32-sketch/petsProject/pull/1)：已通过 merge commit 合并；
- main 候选版本：`6c45883b04851047e5e59412f983096d829df768`；
- [main GitHub Actions #6](https://github.com/fanshengyao32-sketch/petsProject/actions/runs/43186671325)：成功；
- 正式部署：未执行，用户已明确决定不部署；
- 整题最终交付：因缺少自动部署、完整会话导出、视频和群提交等材料，不能判定通过。

## 技术栈

- Go 1.27、Gin、GORM；
- MySQL 8.4 / InnoDB；
- `html/template`；
- 独立模拟支付 HTTP 服务；
- GitHub Actions 质量门禁。

## 已实现业务

- 门店时段发布、容量控制、五分钟占位和并发抢位；
- 预约支付、重复通知、晚到成功异常退回；
- 提前取消、临近取消审核、退款失败及重试；
- 到店核销、服务开始/完成、取消与核销竞争；
- 未到店、店长日清及资金下钻；
- 用户、前台/服务、客服、店长中文业务页面；
- 可信演示会话、角色与门店权限隔离。

## 本地启动

```bash
docker compose up --build
```

启动后访问：

- 主服务：<http://localhost:8080>
- 存活检查：<http://localhost:8080/health/live>
- 就绪检查：<http://localhost:8080/health/ready>
- 模拟支付：<http://localhost:8081/health/live>
- MySQL：`localhost:3307`（容器内 `3306`）

本地配置中的账号和密码只用于教学 Compose 环境，不是正式环境凭据。

## 质量检查

GitHub Actions 在真实 MySQL 五个隔离测试库上执行 gofmt、vet、全量测试、覆盖率门禁、Linux race、Go build、两个容器镜像构建及覆盖率原始文件上传。

## 证据入口

- [需求、任务与门禁](项目文档/03-任务规划/阶段门禁清单.md)
- [独立测试](项目文档/07-独立测试/T34独立复测报告.md)
- [独立重新验收](项目文档/08-独立验收/T34独立重新验收报告.md)
- [Skill 使用汇总](skill-usage.md)
- [最终过程证据索引](项目文档/10-最终交付/过程证据索引.md)
- [AI 使用说明](项目文档/10-最终交付/AI使用说明.md)
- [部署未执行记录](项目文档/09-CI与发布/T39部署未执行记录.md)
