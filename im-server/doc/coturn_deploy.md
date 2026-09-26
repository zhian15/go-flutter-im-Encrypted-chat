# 自建 TURN 服务器部署指南（CentOS / 宝塔）

> 为什么需要：WebRTC P2P 打洞只能穿锥形 NAT；**对称 NAT（企业网/部分 4G/5G，约占 10-20%）必须经 TURN 中继**，否则通话黑屏/无声。STUN 只用来发现公网地址，TURN 负责中继流量。
>
> 硬件要求：1 核 2G 起步，带宽按通话路数预估（视频一路约 1-2 Mbps，TURN 是流量出口）。
> 系统要求：CentOS 7/8/Stream 均可，需 root SSH（宝塔面板仅用来放行端口，coturn 本体走 SSH 装）。

## 一、安装 coturn

```bash
yum install -y epel-release
yum install -y coturn
```

安装后系统会带一个 `turnserver.service`，先设开机自启（先别启动，配置还没写）：

```bash
systemctl enable turnserver
```

## 二、生成长期凭证（用户名/密码）

TURN 不建议匿名开放（会被白嫖流量）。用静态长期凭证最简单：

```bash
# 生成一个随机密码（记下来，后面两处要用）
openssl rand -hex 16
```

## 三、写配置 /etc/turnserver.conf

```bash
cp /etc/turnserver.conf /etc/turnserver.conf.bak
vi /etc/turnserver.conf
```

写入以下内容（**替换 3 处标红项**）：

```ini
# ===== 基础 =====
# 外网网卡名（ip addr 查，一般是 eth0；云服务器上写内网网卡名 + external-ip）
listening-device=eth0
listening-port=3478
# 云服务器：本机只有内网 IP，必须写公网 IP 映射
# 格式：external-ip=<公网IP>/<内网IP>（单公网 IP 直接写 external-ip=公网IP）
external-ip=你的公网IP
# 中继地址：写内网 IP（云服务器）
relay-ip=你的内网IP

# ===== TLS 加密凭证（长期凭证机制）=====
# Realm 随意，一般是域名
realm=im.example.com
server-name=im.example.com
# 静态用户：用户名 turnuser，密码填上面 openssl 生成的（明文即可，别用 lt-cred-mech + 密文表）
user=turnuser:一串随机hex
lt-cred-mech

# ===== 中继端口段（防火墙/安全组必须同步放行）=====
min-port=49152
max-port=65535

# ===== 日志与杂项 =====
no-cli
no-tls
no-dtls
fingerprint
# 不做回环转发，防滥用
no-multicast-peers
no-loopback-peers
```

> 说明：
> - `no-tls` / `no-dtls`：3478 走明文 + 长期凭证。生产想加密可再配证书去掉这两行并加 `cert=/pem 路径 pkey=/key 路径`（TurnsPort 5349），一期不开也安全（凭证本身是挑战应答，不裸奔密码）。
> - **`external-ip` 是云服务器最容易漏的配置**：不写的话，客户端拿到的中继地址是内网 IP，永远连不上，症状是"开了 TURN 还是黑屏"。

## 四、放行端口（宝塔 + 云安全组 两处都要）

**宝塔面板**：安全 → 防火墙 → 放行

| 协议 | 端口 | 用途 |
|---|---|---|
| TCP | 3478 | STUN/TURN 接入 |
| UDP | 3478 | STUN/TURN 接入（**UDP 必须放**，走 TCP 中继质量差） |
| UDP | 49152-65535 | 媒体中继端口段 |

**云厂商控制台**（阿里云/腾讯云安全组）：同样放行以上三条。漏了安全组是最常见的"TURN 配好了但不生效"原因。

```bash
# 命令行方式放行（如果不用宝塔 UI）
firewall-cmd --permanent --add-port=3478/tcp
firewall-cmd --permanent --add-port=3478/udp
firewall-cmd --permanent --add-port=49152-65535/udp
firewall-cmd --reload
```

## 五、启动与验证

```bash
systemctl start turnserver
systemctl status turnserver        # active (running) 即可
# 看实时日志排错
journalctl -u turnserver -f
```

**验证 1：端口监听**

```bash
ss -lnup | grep 3478    # UDP 监听
ss -lntp | grep 3478    # TCP 监听
```

**验证 2：从你本地电脑测试打通**（Trickle ICE 网页最直观）

1. 打开 https://webrtc.github.io/samples/src/content/peerconnection/trickle-ice/
2. 填入 STUN/TURN URL：`turn:你的公网IP:3478`，用户名 `turnuser`，密码填配置里的
3. 点 Gather candidates
4. 结果里出现 **`relay` 类型**的 candidate → TURN 完全正常；只有 `srflx` 没有 `relay` → 凭证错或端口没放行

**验证 3：命令行（可选）**

```bash
# 服务器上自己测
turnutils_uclient -u turnuser -w 一串随机hex -p 3478 你的公网IP
# 输出 total relay packets>0 即成功
```

## 六、填进 IM 后台

管理后台 → 系统配置 → 音视频：

1. 通话引擎选「WebRTC」
2. ICE 服务器填 JSON（把 IP/凭证换成你的）：

```json
[
  { "urls": "stun:你的公网IP:3478" },
  { "urls": "turn:你的公网IP:3478", "username": "turnuser", "credential": "你的随机hex" }
]
```

保存后，客户端下次发起通话即走 WebRTC + 你的 TURN。

## 常见问题

| 现象 | 原因 |
|---|---|
| Trickle ICE 有 srflx 无 relay | 凭证错误 / `lt-cred-mech` 没配 / 用户名密码不匹配 |
| 本机测通，外网无 relay | 云安全组或防火墙没放行 3478 / 中继端口段 |
| relay 有了但通话黑屏 | `external-ip` 没配公网 IP（客户端拿到内网中继地址） |
| 时通时不通 | 只放行了 TCP 3478，UDP 没放；或中继端口段只放了一部分 |
