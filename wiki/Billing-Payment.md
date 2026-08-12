# 计费与支付

[← Wiki 首页](Home.md)

完整支付配置请以 [docs/PAYMENT_CN.md](../docs/PAYMENT_CN.md) 为准；本文是概念与操作索引。

---

## 计费模型（概念）

```text
请求完成 → 解析 input/output tokens（及缓存等）
         → 查模型单价 × 分组/用户倍率
         → 写入 UsageLog
         → 扣用户余额 或 计入订阅/平台配额
```

相关配置：

- `pricing` — 价格表来源（本地 JSON / 远程）
- `billing.circuit_breaker` — 计费异常时 fail-closed，避免白嫖
- 分组倍率、用户级倍率覆盖
- 平台日/周/月配额（`UserPlatformQuota`）

简易模式（`run_mode=simple`）会跳过计费校验，仅适合受信内网。

---

## 资金与权益入口

| 方式 | 说明 |
|------|------|
| 管理员调余额 | 后台用户详情直接加减 |
| 兑换码 Redeem | 余额 / 时长等类型批量发放 |
| 优惠码 Promo | 注册或充值优惠 |
| 在线支付 | EasyPay、支付宝官方、微信官方、Stripe |
| 订阅计划 | 套餐商品 + 用户订阅记录 |

---

## 内置支付（摘要）

1. 管理后台 → **设置 → 支付设置** → 启用支付  
2. 配置金额上下限、超时、待支付订单上限、负载均衡策略  
3. **服务商管理** 添加实例（易支付 / 支付宝 / 微信 / Stripe）  
4. 为前台「支付宝」「微信支付」按钮各指定唯一来源（官方或易支付）  
5. 配置 Webhook 回调 URL 到公网可访问地址  

用户侧：充值页下单 → 扫码/跳转 → Webhook 到账 → 余额更新。

管理侧还可配置支付相关套餐页、订单审计、退款申请等。  
对接外部系统见 [docs/ADMIN_PAYMENT_INTEGRATION_API.md](../docs/ADMIN_PAYMENT_INTEGRATION_API.md)。

---

## 兑换码运维提示

- 批量生成时使用幂等键，避免支付回调重复下发  
- Admin CLI：`redeem-codes generate` / `create-and-redeem`  
- 导出与审计注意权限分离  

---

## 相关

- [管理后台](Admin-Guide.md)
- [用户使用](User-Guide.md)
- [配置参考](Configuration.md)
