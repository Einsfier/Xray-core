> This is a modified Xray-core maintained by myself.
>
> Updates from the upstream will be merged periodically.

<h3>It has several added features:</h3>

* OSPFv2 support when act as an on-demand transparent proxy.
* DNS-route capability.
* Conn-track capability for routing decision.
* HTTP health-check inbounds.

If you have any questions which are not related to features described above,
please submit it to [upstream project](https://github.com/XTLS/Xray-core).

### 更新记录

* 2026/04/21: 修复DNS UDP Server在负载均衡模式下，长期复用同一条连接导致无法自动选择最优出口的问题。引入`NoCacheDispatcher`，超过容忍时间后自动新建连接以触发负载均衡重新选路。
* 2026/04/10: 从v2ray-core移植dnsCircuit模块至Xray-core。新增`multiObservatory`配置支持（允许为不同负载均衡器配置独立的`burst`/`default`类型观测器）。修复`leastload`策略中`tolerance`参数不生效的问题（原实现未读取该配置值，现已正确按失败率过滤节点）。修改Observatory gRPC Command服务：`GetOutboundStatusRequest`新增`tag`字段支持按tag查询；当使用`multiObservatory`时，自动聚合所有子观测器的结果返回；同时注册了v2ray兼容的gRPC ServiceName以支持现有客户端。
* 2024/08/15: 添加了statsServer配置，可配合个人修改版的[v2ray-exporter](https://github.com/povsister/v2ray-exporter)/prometheus/grafana观测Xray出口及客户端实时流量或趋势。v2ray-exporter已新增Observatory指标采集支持（存活状态、延迟、健康探测统计等），详见[v2ray-exporter仓库](https://github.com/povsister/v2ray-exporter)。
* 2024/08/02: 添加了负载均衡的配置示例，用于简化使用负载均衡作为出口时，conn-track规则书写繁琐的问题。提高配置可维护性。

<div>
  <br>
  <h1>Project X - Xray</h1>
  <p>Project X is a set of network tools that helps you to build your own computer network. It secures your network connections and thus protects your privacy.</p>
</div>

<!-- TOC -->
* [更新记录](#更新记录)
* [Related Links](#related-links)
* [为什么开发本项目](#为什么开发本项目)
  * [方案差异对比](#方案差异对比)
  * [原理解释](#原理解释)
* [前置要求](#前置要求)
  * [理论知识](#理论知识)
  * [硬件要求](#硬件要求)
* [使用说明](#使用说明)
  * [0x1: 网络拓扑配置](#0x1-网络拓扑配置)
  * [0x2: 旁路由（Xray）配置](#0x2-旁路由xray配置)
    * [安装Xray并配置透明代理](#安装xray并配置透明代理)
      * [赋予Xray额外权限，用于支持OSPF协议](#赋予xray额外权限用于支持ospf协议)
    * [配置Xray的OSPF模块](#配置xray的ospf模块)
    * [配置IP masquerade](#配置ip-masquerade)
    * [收尾工作，开机自启，配置持久化](#收尾工作开机自启配置持久化)
      * [持久化开启内核IPv4转发参数](#持久化开启内核ipv4转发参数)
      * [Xray开机自启动](#xray开机自启动)
      * [将nftables的配置持久化](#将nftables的配置持久化)
  * [0x3: 主路由（ROS）配置](#0x3-主路由ros配置)
    * [开启OSPF动态路由协议](#开启ospf动态路由协议)
      * [ROSv6系统配置OSPF](#rosv6系统配置ospf)
      * [ROSv7系统配置OSPF](#rosv7系统配置ospf)
    * [配置策略路由以避免路由环路](#配置策略路由以避免路由环路)
    * [配置DNS转发旁路由](#配置dns转发旁路由)
    * [配置探活和探活失败时自动回切DNS的脚本](#配置探活和探活失败时自动回切dns的脚本)
      * [ROSv6旁路由探活脚本](#rosv6旁路由探活脚本)
      * [ROSv7旁路由探活设置](#rosv7旁路由探活设置)
* [Xray配置示例](#xray配置示例)
    * [Xray监控预览](#xray监控预览)
    * [大陆白名单+全局代理配置示例](#大陆白名单全局代理配置示例)
* [FAQs](#faqs)
  * [OSPF 收敛速度快吗？](#ospf-收敛速度快吗)
  * [这个修改版的Xray为什么关闭有点慢](#这个修改版的xray为什么关闭有点慢)
* [Stargazers over time](#stargazers-over-time)
<!-- TOC -->

# Related Links

- [Documentation](https://xtls.github.io) and [Project X Official Website](https://xtls.github.io)
- [Xray-core GitHub](https://github.com/XTLS/Xray-core)

# 为什么开发本项目

先叠个甲，本方案配置较为繁琐，有硬件要求，且深度涉及计算机网络原理，仅适用于有进阶网络知识的用户使用。

本项目旨在解决：使用透明代理进行网关科学的模式下，网关直接使用软路由带来的稳定性问题，以及性能问题。

如果你不在意：默认使用软路由作为你的家庭网关，且可以接受折腾软路由时造成全部网络中断/抖动的问题，则本文方案可能不适合你。

**核心理念为：仅需要科学的流量会被转发至软路由处理，其余流量由主路由直接发出。
主路由使用常规硬路由以保证性能和稳定性。**

因此，对于旁路由的性能要求降到了一个非常低的水平，同时，软路由的任何故障对于网络的影响也基本消除。

类似的，以"按需转发流量"作为核心理念的方案有：[FakeDNS](https://www.v2fly.org/config/fakedns.html)。
我也试用过相当长一段时间，但其存在几个我无法接受的问题：

* FakeIP污染，例如：大陆白名单时，默认污染其他所有域名
* 旁路由故障/修改配置重启时，FakeIP污染会持续一段时间无法立即清除
* 需要手动在主路由上维护静态路由条目
* 无法灵活应对Telegram这种不使用系统DNS的软件
* 旁路由入侵网络拓扑，无法快速移除

相比之下，本方案具有以下优点：

* 全真IP，不存在FakeIP污染，同时解决国内环境的DNS污染
* 支持基于规则文件的IP路由规则，灵活应对Telegram类似的软件
* 分流黑白名单模式可按喜好配置，无任何副作用
* 默认支持 srcIP -> dstIP 作为pattern的Connection-track，路由决策无需Sniffing
* 旁路由可插拔，生效路由条目由旁路由自动通告，无需维护静态路由条目，网络拓扑可自动容灾
* 整体方案扩展能力强，可结合硬路由和软路由的各自特点，并充分利用各自的优势

## 方案差异对比

| 特性    | 软路由           | 硬路由           | 本方案（按需旁路）                 |
|-------|---------------|---------------|---------------------------|
| 科学能力  | 强，取决于ROM      | 弱，配置复杂+不灵活    | 强，包含所有Xray功能              |
| 性能    | 取决于硬件配置       | 远强于同规格软路由     | 强，直连性能等同于硬路由，科学性能取决于软路由配置 |
| 功耗    | 高             | 低             | 较低                        |
| NAT情况 | 取决于软件承诺       | 一般为FullClone  | 直连流量与硬路由无异，科学流量取决于Xray承诺  |
| 容灾    | 无，全部断网        | 配置复杂          | 自动恢复拓扑，科学流量可降级为直连         |
| 稳定性   | 低，重启/故障影响全部网络 | 高，仅受不可抗力影响    | 高，旁路由故障/重启不影响主干网络         |
| 扩展能力  | 低下限高上限，取决于ROM | 高下限，支持各种电信级玩法 | 高，可充分利用软硬路由各自优势           |

## 原理解释

下面的拓扑图表示了本项目中旁路由的工作方式。

图示过程描述了一台内网设备是如何在

* 不修改默认网关
* 不修改默认DNS
* 不安装代理软件

的情况下，无感知的通过网关透明代理，科学访问www.google.com的。

粉色的箭头表示DNS请求流程，绿色的箭头表示真正的访问流程（即实际传输数据的TCP/UDP过程）。

简单来说，主路由会把所有来自LAN的DNS请求，通过防火墙的DNAT规则转发给旁路由处理，
**旁路由会使用DNS请求的域名+DNS解析结果，预先进行一次路由决策**：

若域名+DNS解析后的IP

* 匹配代理出口的Tag：通过OSPFv2动态路由协议，**向主路由通告目标IP的下一跳为旁路由**，返回DNS解析结果，同时添加基于 srcIP ->
  dstIP 的conn-track规则
* 不匹配代理Tag：直接返回DNS解析结果即可

这一过程被我称作DNS Route：通过分析来自客户端的DNS请求，按需产生一条通往目标域名IP的路由规则。
同时，得益于动态添加的，基于源-目标IP的conn-track规则，后续连接出口的匹配可以直接跳过Xray的Sniffing，在ECH普及的未来仍可做到精准域名分流。

而且，由于科学访问的路由表由OSPF动态路由协议维护，旁路探活失败时主路由会自动恢复网络拓扑，
配合探活脚本自动回切防火墙的DNS转发规则，则可以完美的消除旁路故障对于主干网络的影响。

目前实现中，DNS Route的掩码为/32，有效时间默认为24个小时（可通过`inactiveClean`配置），在有效期内没有任何DNS请求或者实际流量，则会自动废弃对应路由条目。
实际使用中，生效路由条目约为400-600条，配合fastTrack，对于主路由的性能影响可以忽略不计。

![How it works](/images/howItWorks.png)

# 前置要求

本项目中，Xray将被配置为旁路由透明代理使用，需要你事先掌握/具备以下条件：

## 理论知识

* 理解什么是透明代理
* 如何配置Xray以透明代理模式工作
* 理解单臂路由（旁路由）的基本工作原理
* 理解路由设备的工作原理，熟悉路由决策过程，理解路由表及防火墙基本原理
* 熟悉nftables，具备基本的linux操作能力

## 硬件要求

* 一台支持OSPFv2动态路由协议的主路由，且主路由需要支持策略路由（某些文章可能称为标记路由）。
* 一台可运行Xray的Debian Linux作为单臂旁路由

**以下的使用说明中，采用的硬件配置为**

主路由（ROSv6）：MikroTik hAPac2 RBD52G-5HacD2HnD (RouterOS v6.49.14)

主路由（ROSv7）：MikroTik RB5009UG+S+IN (RouterOS v7.15.0)

旁路由：Debian 12 Linux with 2-core 2GiB RAM (LXC PVE v8.2.4 on N100)

推荐主路由使用ROS，旁路由使用Debian11及以上的linux系统，至少分配1c1g的资源。

# 使用说明

## 0x1: 网络拓扑配置

请参考如下拓扑，配置好主路由与旁路由。

**核心诉求只有两点：**

* 主路由与旁路由需要和LAN设备隔离出一个网段，且这个网段只有主路由和旁路由两个设备，这个是必须要求。
* 主路由与旁路由IP固定

![Network topology](/images/topology.png)

## 0x2: 旁路由（Xray）配置

简单来说，旁路由配置主要有以下步骤：配置透明代理，配置OSPF相关参数/健康检查端口，配置IP masquerade等。

以下所有命令中 `${IFNAME}` 均代表软路由和主路由连接的网卡名称，可以使用 `ifconfig` `ip link` 等命令查看。

使用时需要替换成你自己环境中的网卡名称。

### 安装Xray并配置透明代理

linux系统上推荐使用 [Xray-install](https://github.com/XTLS/Xray-install)
下载并安装Xray

然后下载[本项目Release页](https://github.com/Einsfier/Xray-core/releases)
里的修改版，替换xray可执行文件即可，xray的默认安装路径为 `/usr/local/bin/xray`

[透明代理的配置教程](https://xtls.github.io/document/level-2/tproxy.html)
已经很多，我就不再赘述了，请参考已有教程自行完成透明代理的nftables配置，核心要求只有以下几个：

* 只支持TPROXY模式的透明代理，请勿配置成REDIRECT模式。
* 需要拦截UDP53的DNS查询请求，并转交给Xray内置DNS处理
* 强烈推荐替换Xray的默认geoip/geosite规则文件为社区增强版本的 [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat)

  Xray的默认dat文件路径为`/usr/local/share/xray`，下载对应dat文件直接替换即可。
* 需要参考本节末尾，**额外赋予 Xray `NET_RAW` 权限**，否则无法正常收发OSPF数据包

另外，[建议按照教程要求，修改xray的最大文件描述符限制](https://xtls.github.io/document/level-2/tproxy.html)，
避免在处理UDP流量时出现问题。

#### 赋予Xray额外权限，用于支持OSPF协议

在 `/etc/systemd/system/xray.service.d/11-extra-capability.conf` 里创建以下内容

```shell
[Service]
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
```

保存并退出，执行 `systemctl daemon-reload` 以便配置生效

### 配置Xray的OSPF模块

此模块为本项目完全独立开发的部分，得益于Xray良好的模块化设计，最终以`dnsCircuit`模块形式嵌入了Xray中，需要在配置文件中写入指定配置方可开启。

Xray的默认配置文件路径为 `/usr/local/etc/xray/config.json`

不开启此模块时，此修改版的Xray与官方版本无异。

配置文件示例（节选），仅展示本项目新增的核心配置项，完整配置请参考后文。

* `dnsCircuit` 部分，**是本项目核心的功能模块**，用于启用DNS Route，并配置要监听的inbounds/outbounds/balancers等
* `inbounds` 中需要配置一个HTTP健康检查入口，用于主路由探活

```json5
{
  "dnsCircuit": {
    //（必填）DNS outbound 的Tag
    "dnsOutboundTag": "dns-out",
    //（必填）用于conn-track的inbound，填写透明代理的inboundTag
    "inboundTags": ["transparent"],
    //（outboundTags和balancerTags二选一即可）
    // outboundTags 匹配单个出口，balancerTags 匹配负载均衡器
    // 使用负载均衡时填 balancerTags，不使用时填 outboundTags
    "balancerTags": ["balancer-proxy-us", "balancer-proxy-jp"],
    //（可选）固定通告的IP段，不受 inactiveClean 清理，仅支持IPv4
    "persistentRoute": [
      "geoip:telegram",
      "10.0.0.0/8"
    ],
    //（可选）不活跃路由清理时间（秒），默认86400（24小时）
    "inactiveClean": 172800,
    //（必填）OSPF设置，子网掩码须在 /24~/32
    "ospfSetting": {
      "ifName": "eth0",
      "address": "192.168.66.2/24"
    }
  },
  "inbounds": [
    {
      //（必填）健康检查入口，listen必须为0.0.0.0，protocol必须为http-healthcheck
      "tag": "health-check",
      "listen": "0.0.0.0",
      "port": 54321,
      "protocol": "http-healthcheck",
      "settings": { "timeout": 3 }
    },
    {
      //（必填）透明代理入口，必须使用TPROXY模式
      "tag": "transparent",
      "listen": "127.0.0.1",
      "port": 12345,
      "protocol": "dokodemo-door",
      "settings": { "network": "tcp,udp", "followRedirect": true },
      "sniffing": { "enabled": false },
      "streamSettings": { "sockopt": { "tproxy": "tproxy", "mark": 255 } }
    }
    // ...
  ],
  "outbounds": [
    // ... direct / proxy / dns-out 等，详见完整配置示例
  ],
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      // ... 常规路由规则省略，详见完整配置示例
      {
        //（重要，必填）conn-track规则，紧跟在域名路由规则之后
        // 每个 dnsCircuit 中声明的 outboundTag/balancerTag 都需要一条对应规则
        "type": "field",
        "source": "dynamic-ipset:dnscircuit-conntrack-src-proxy",
        "ip": "dynamic-ipset:dnscircuit-conntrack-dest-proxy",
        "outboundTag": "proxy"
      },
      {
        //（重要，必填）dnsCircuit默认路由兜底，建议放在所有路由规则最后
        "type": "field",
        "ip": "dynamic-ipset:dnscircuit-dest-default",
        "outboundTag": "proxy"
      }
    ]
  },
  "dns": {
    // 需要配置国内外DNS分流，详见完整配置示例
  }
}
```

### 配置IP masquerade

此配置的主要目的是，配合主路由上的策略路由规则，直接转发透明代理不处理的IP数据包（即，TCP/UDP协议以外的IP报文），
以及直接送出旁路由本身发出的流量，避免形成路由环路。

直接按要求设置即可，主路由配置时会再提到这部分。

运行命令，开启内核的IPv4包转发功能，并设置从旁路由网卡发出的IP包做masquerade，注意替换`IFNAME`为你自己的网卡名称。

```shell
# 开启内核IPv4包转发
sysctl -w net.ipv4.ip_forward=1

# 添加一个名为xray的table
# 如果你在配置透明代理时已经添加过这个table
# 则这个命令不出意外会执行失败，也可以直接跳过
nft add table xray

# 在table xray的POSTROUTING链上挂一个nat hook
nft add chain xray postrouting { type nat hook postrouting priority 0 \; }
# 向xray POSTROUTING链中添加一条规则，从 ${IFNAME} 网卡发出的流量全部进行masquerade
nft add rule xray postrouting oif ${IFNAME} masquerade
```

### 收尾工作，开机自启，配置持久化

主要是内核参数，透明代理策略和nftables的规则持久化。

#### 持久化开启内核IPv4转发参数

编辑 `/etc/sysctl.conf`，添加`net.ipv4.ip_forward=1`，
执行命令

```shell
echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf
```

#### Xray开机自启动

执行 `systemctl enable xray` 即可

然后用 `systemctl status xray` 确认设置，有出现enabled字样即可

```shell
root@debian:~# systemctl status xray
● xray.service - Xray Service
     Loaded: loaded (/etc/systemd/system/xray.service; enabled; vendor preset: enabled)
    Drop-In: /etc/systemd/system/xray.service.d
             └─10-donot_touch_single_conf.conf, 11-extra-capability.conf, 20-ulimit.conf
     Active: active (running) since Thu 2024-05-09 23:16:40 HKT; 17h ago
   Main PID: 195546 (xray)
      Tasks: 9 (limit: 2337)
     Memory: 215.3M
        CPU: 7min 8.496s
     CGroup: /system.slice/xray.service
             └─195546 /usr/local/bin/xray run -config /usr/local/etc/xray/config.json
```

#### 将nftables的配置持久化

首先，检查nftables配置，运行命令 `nft list ruleset`

你的配置应该和下面的输出类似，注意不要照抄。按自己实际情况确认。

**⚠️：以下nftables配置包含了透明代理（TProxy）的配置，本教程中未直接列出透明代理的配置命令，请查阅上文自行进行透明代理的配置。**

**⚠️：注意透明代理中`OUTPUT Chain`的配置，其目的是拦截旁路由本身主动向外发出的流量并代理，但在一些特殊情况下会导致一些预期外的行为，包括但不限于：**
* **有公网IPv4的情况下，主路由暴露端口，并直接DNAT到旁路由的某一端口。会发现旁路由不响应任何来自公网IP的连接请求，其原因是响应报文被`OUTPUT Chain`拦截所致。**
  
  **解决办法是：修改`OUTPUT Chain`规则，使其只拦截本机的UDP 53的DNS查询流量即可，但也会导致旁路由本身不在透明代理范畴内，不过基本没什么影响。有需要的可按照上文所述自行调整nftables规则，此处不再赘述。**

```shell
root@debian:~# nft list ruleset
table inet filter {
	chain input {
		type filter hook input priority filter; policy accept;
	}

	chain forward {
		type filter hook forward priority filter; policy accept;
	}

	chain output {
		type filter hook output priority filter; policy accept;
	}
}
table ip xray {
	chain prerouting {
		type filter hook prerouting priority filter; policy accept;
		ip daddr { 127.0.0.1, 224.0.0.0/4, 255.255.255.255 } return
		meta l4proto tcp ip daddr 192.168.0.0/16 return
		ip daddr 192.168.0.0/16 udp dport != 53 return
		meta mark 0x000000ff return
		meta l4proto { tcp, udp } meta mark set 0x00000001 tproxy to 127.0.0.1:12345 accept
	}

	chain output {
		type route hook output priority filter; policy accept;
		ip daddr { 127.0.0.1, 224.0.0.0/4, 255.255.255.255 } return
		meta l4proto tcp ip daddr 192.168.0.0/16 return
		ip daddr 192.168.0.0/16 udp dport != 53 return
		meta mark 0x000000ff return
		meta l4proto { tcp, udp } meta mark set 0x00000001 accept
	}

	chain postrouting {
		type nat hook postrouting priority filter; policy accept;
		oif "eth0" masquerade
	}
}
table ip filter {
	chain divert {
		type filter hook prerouting priority mangle; policy accept;
		meta l4proto tcp socket transparent 1 meta mark set 0x00000001 accept
	}
}
```

确认无误后，保存规则至 `/etc/nftables/rules.v4`，需要执行以下命令

`nft list ruleset > /etc/nftables/rules.v4`

然后，新建systemd service，在 `/etc/systemd/system/tproxy.service` 创建以下内容，
目的是通过systemd管理自启任务。

```shell
[Unit]
Description=Tproxy rule
After=network.target
Wants=network.target

[Service]

Type=oneshot
RemainAfterExit=yes
ExecStart=/sbin/ip rule add fwmark 1 table 100 ; /sbin/ip route add local default dev lo table 100 ; /sbin/nft -f /etc/nftables/rules.v4
ExecStop=/sbin/ip rule del fwmark 1 table 100 ; /sbin/ip route del local default dev lo table 100 ; /sbin/nft flush ruleset

[Install]
WantedBy=multi-user.target
```

设置开机自启动，执行 `systemctl enable tproxy`即可。

## 0x3: 主路由（ROS）配置

主路由配置基本是四块：开启OSPF动态路由协议，防止路由环路，DNS转发，以及旁路由探活和探活失败时自动回切DNS的脚本。

**主路由怎么配置正常上网我就不赘述了，本文默认你已经会使用ROS配置PPPoE拨号或者直接DHCP上网。**

### 开启OSPF动态路由协议

由于v6和v7的OSPF配置差别过大，下面会同时给出两种系统的配置示例。只支持OSPFv2，即IPv4协议。

#### ROSv6系统配置OSPF

进入 `Routing -> OSPF` 菜单，如果是v7的ROS系统，参考下面选OSPFv2，本项目目前只支持IPv4

进入 `Interfaces`，选择和旁路由直接相连的接口，我这里旁路由和主路由接口都属于一个网桥，所以直接选网桥即可，如果你没用网桥，那就选接口。
验证选None，不开启验证，优先级填1，其他默认即可，务必保证`HelloInterval=10` 且 `RouterDeadInterval=40`，否则会影响邻接。
![ospf interfaces](/images/ospf-interfaces.png)

进入 `Instances`，填写主路由的RouterID，这里直接写主路由相对于旁路由网段的IP地址即可，例如在我的拓扑中，这里填写主路由IP `192.168.66.1`。
其他全默认即可，见下图
![ospf instances](/images/ospf-instances.png)

进入 `Network`，填写主路由和旁路由所属的网段以及掩码，Area选择默认的backbone即可，如下图所示
![ospf networks](/images/ospf-networks.png)

至此完成OSPF配置，等待40秒后，你的主路由 `Interface - State` 应该和上图一样，展示为 Designated Router（即DR）状态。

#### ROSv7系统配置OSPF

进入 `Routing -> OSPF` 菜单，先进入`Instances`，新建一个OSPFv2的实例，注意下图的红框内容。

RouterId填写你主路由和旁路由通信的IP地址即可，图中仅供参考不要照抄。
![rosv7 ospf instances](/images/rosv7-ospf-instance.png)

进入 `Areas`，填写backbone区域，instance选择上一步创建的instance，`Area ID`和其他内容照图填写。
![rosv7 ospf area](/images/rosv7-ospf-area.png)

进入 `Interface Templates`，选择和旁路由相连的接口名称，选择刚刚创建的area，Networks填写你规划的主路由和旁路由的网段。
其余配置照图填写
![rosv7 ospf interface](/images/rosv7-ospf-interface.png)

至此完成OSPF配置，等待40秒后，你的主路由 `Interfaces`中，应该会出现一个state为DR（Designated Router）动态条目。代表OSPF配置成功。

### 配置策略路由以避免路由环路

因为透明代理只能处理TCP和UDP流量，其他类型的IP数据包会由linux内核直接转发，
而主路由上的OSPF动态路由表，会无条件将所有OSPF通告目标IP的数据报文下一跳给旁路，旁路由的默认网关又是主路由。
因此，在极少数情况下，这个互相甩锅的过程，会造成路由环路的问题。

当然，Xray配置有误也会导致环路，这个暂且按下不表。

为了避免环路，需要识别出旁路由发出的流量，跳过OSPF的动态路由规则进行匹配。

这块就需要旁路由转发IP报文时，无条件做IP masquerade，然后，用主路由的策略路由功能进行分流，具体步骤为：

* **主路由创建一个新的路由表**，记为`side-anti-loop`，此路由表中需要填写默认路由为WAN口，以及本地LAN IP段所属的网桥或接口。

  注意红框中的内容，如果你本地有其他网段，需要一并以静态路由形式填入，注意选择所属接口。这块照图自己写吧，就不给命令了。

  ⚠️：如果使用ROSv7系统，需要到`Routing -> Tables`新建路由表，才能在`IP -> Routes`中使用，注意新建的路由表也要勾选FIB。

  新建的路由表中，其所有条目状态（最前面的字母），应该为`AS`，即 `active & static`，如果状态不对请自行排查。
  ![New RoutingTable: side-anti-loop](/images/side-rtable.png)


* **主路由创建策略路由规则**：来自于旁路由IP 192.168.66.2的数据包，仅查询路由表`side-anti-loop`

  ROSv6的对应命令如下，其中`side-router`是我旁路由所在的网桥，`192.168.66.2`是我的旁路由IP，你可以视情况改成接口/你自己的旁路由IP。不要照抄。
  ```shell
  /ip route rule add src-address=192.168.66.2 interface=side-router action=lookup-only-in-table table=side-anti-loop 
  ```

  ⚠️：ROSv7系统的策略路由配置在`Routing -> Rules`菜单中，对应命令示例如下，注意不要照抄，`src-address`和`interface`要视情况改成你自己的旁路由IP和接口。
  ```shell
  /routing rule add src-address=192.168.66.2 interface=side-router action=lookup-only-in-table table=side-anti-loop 
  ```

至此，你应该已经完成了主路由的策略路由配置：所有来自于旁路由IP的数据包，将仅查询`side-anti-loop`这个路由表，
甚至包括Xray配置错误时（例如：错误的将应该代理的流量直连发出）也不会环路，从根本上避免了路由环路的产生。

**⚠️：尽量使用策略路由，在Firewall使用mark-routing（标记路由）很容易会遇到FastTrack不兼容问题，需要额外配置mark-connection/packet较为麻烦**

### 配置DNS转发旁路由

这一步的作用是，主路由拦截所有内网设备发出的DNS请求，并将其转发给旁路由，由旁路由解析并返回，同时做DNS Route决策。
对于整个项目的目标来说，是至关重要的一步，其主要目的是：

* 嗅探内网设备要访问的域名，提前建立路由表转发规则，达成按需转发流量的目的
* 内网设备零配置，对于科学上网完全无感知
* 科学或者旁路故障切换时，仅网关进行切换即可

所以这里的配置就很简单了，只需要排除来源为旁路由IP的DNS查询流量，
然后将所有目的为UDP53的流量DNAT给旁路由即可。

直接上命令，添加DNAT rule，注意替换目标IP为你旁路由的IP，以及，**注意一定要给这条规则，添加注释为：DnsForward**，
这条注释会用作下面探活切换时，防火墙的DNAT规则匹配。

```shell
/ip firewall nat add chain=dstnat protocol=udp dst-port=53 src-address=!192.168.66.2 action=dst-nat to-addresses=192.168.66.2 to-ports=53 comment="DnsForward"
```

**⚠️ 注意，如果你的ROS具有公网地址，则该DNAT配置会导致WAN口UDP53公网可访问，且会响应DNS查询请求。**

这会导致Xray的DnsRoute里出现比较奇怪的来源IP记录。要禁止接受WAN口DNS请求，
需要在Firewall - Filter - Forward chain，添加 DST UDP53 且in-interface-list WAN action DROP的规则即可。不再赘述。

### 配置探活和探活失败时自动回切DNS的脚本

这一步是配置旁路由故障时的自动容灾措施，目的是在旁路由故障时，自动切换DNS为ISP默认DNS，保持主干网络完全可用。

还记得之前在Xray的inbounds里，建立了一个protocol名为`http-healthcheck`的代理入口么，那就是本项目用来探测Xray实例是否正常工作的探活端点。

相比于IP探活，HTTP探活直接检测了代理软件的存活情况，更加精准可靠。

**以下内容仅适用于ROSv6的系统，v7的系统可以直接使用`Tools -> Netwatch`，直接配置旁路由IP+探活端口，HTTP方式探活即可。**

#### ROSv6旁路由探活脚本

在ROS的 `System -> Scripts` 菜单中，创建一个名为 `probeSide` 的脚本，内容填写下面的代码。
注意端口号要和Xray配置中的探活端口号一致。

```shell
do {
  :local result [/tool fetch url=("http://health-check.side.local:54321/health") mode=http duration=10s output=user as-value];
  :if ($result->"status" = "finished") do={
    :if ([/ip firewall nat get [/ip firewall nat find where comment="DnsForward"] disabled]) do={
      /log info "Side-Router health probe OK - Turn ON DNS Forward";
      /ip firewall nat enable [/ip firewall nat find where comment="DnsForward"];
      /ip dns set allow-remote-requests=no;
    }
  }
} on-error={
  :if (![/ip firewall nat get [/ip firewall nat find where comment="DnsForward"] disabled]) do={
    /log info "Side-Router health probe FAILED - Turn OFF DNS Forward";
    /ip firewall nat disable [/ip firewall nat find where comment="DnsForward"];
    /ip dns set allow-remote-requests=yes;
  }
}
```

然后，在 `IP -> DNS -> Static` 菜单中填入一个静态DNS记录，指向旁路由IP。注意域名不要填错，以及旁路由IP填你自己的IP。

```
health-check.side.local  192.168.66.2
```

最后，在 `System -> Scheduler` 中创建一个定时任务，设置间隔为一分钟，执行下面的命令即可

```shell
/execute script="probeSide"
```

#### ROSv7旁路由探活设置

ROSv7可直接使用`Tools - Netwatch`新建探活任务，照下图设置即可，注意host和port填写旁路由的IP地址和探活端口。

然后，别忘了在`UP`和`DOWN`事件的脚本里填写如下内容

**On Up**
```shell
/log info "Side-Router health probe OK - Turn ON DNS Forward";
/ip firewall nat enable [/ip firewall nat find where comment="DnsForward"];
/ip dns set allow-remote-requests=no;
```

**On Down**
```shell
/log info "Side-Router health probe FAILED - Turn OFF DNS Forward";
/ip firewall nat disable [/ip firewall nat find where comment="DnsForward"];
/ip dns set allow-remote-requests=yes;
```

至此，你已经完成了主路由和旁路由的拓扑配置，下一步，是时候完成Xray的完整配置了。

# Xray配置示例

理论上，本方案中Xray可按喜好配置GFW黑名单，或者大陆白名单代理模式。
以下示例采用大陆白名单模式：已知国内域名和DNS解析结果为国内IP的域名直连，其余所有流量走代理。

**⚠️ 本项目基于Xray-core，支持Xray JSON配置格式**

### Xray监控预览
![Xray Dashboard](/images/v2ray-dashboard.png)

![Xray Dashboard p2](/images/v2ray-dashboard-p2.png)

### 大陆白名单+全局代理配置示例

以下是一个基于实际使用的完整Xray配置示例，采用大陆白名单模式（已知国内域名/IP直连，其余全部走代理），包含：
* 国内外DNS分流 + 大陆DNS优先尝试 + 海外DNS兜底
* 多区域代理出口（US/TW/HK/JP/SG）+ 负载均衡
* burst观测器 + leastload策略
* dnsCircuit + OSPF动态路由

**⚠️ 重要：必须将所有代理节点的域名显式配置为直连（DNS和路由均需配置），否则代理节点自身的流量也会被转发至旁路由，导致路由环路。**
配置中标注 `Auto-Generated DIRECT-DOMAIN` 的条目即为此用途，请确保你的所有代理服务器域名都包含在内。

请根据自己需求酌情修改。

```json5
{
  "log": {
    "loglevel": "warning",
    "dnsLog": false
  },
  "api": {
    "tag": "api",
    "services": ["StatsService", "ObservatoryService"]
  },
  "stats": {},
  "policy": {
    "system": {
      "statsOutboundUplink": true,
      "statsOutboundDownlink": true
    }
  },
  // burst 类型观测器，为每个负载均衡器提供独立的节点健康数据
  "multiObservatory": {
    "observers": [
      {
        "type": "burst",
        // 此 tag 需要和 balancer.strategy.settings.observerTag 对应
        "tag": "observatory-internet-default",
        "settings": {
          // 前缀匹配，"proxy-us:" 会匹配所有以 "proxy-us:" 开头的 outbound tag
          "subjectSelector": ["proxy-us:", "proxy-tw:", "proxy-hk:"],
          "pingConfig": {
            "destination": "https://www.gstatic.com/generate_204",
            "interval": "15s",
            "sampling": 20,
            "timeout": "5s"
          }
        }
      },
      {
        "type": "burst",
        "tag": "observatory-internet-jp",
        "settings": {
          "subjectSelector": ["proxy-jp:", "proxy-sg:"],
          "pingConfig": {
            "destination": "https://www.gstatic.com/generate_204",
            "interval": "15s",
            "sampling": 20,
            "timeout": "5s"
          }
        }
      }
    ]
  },
  "inbounds": [
    {
      "tag": "api",
      "listen": "127.0.0.1",
      "port": 11451,
      "protocol": "dokodemo-door",
      "settings": { "address": "127.0.0.1" }
    },
    {
      "tag": "health-check", // 用作代理软件健康检查
      "listen": "0.0.0.0",
      "port": 54321,
      "protocol": "http-healthcheck",
      "settings": { "timeout": 3 }
    },
    {
      "tag": "transparent",
      "listen": "127.0.0.1",
      "port": 12345,
      "protocol": "dokodemo-door",
      "settings": {
        "network": "tcp,udp",
        "followRedirect": true
      },
      "sniffing": { "enabled": false },
      "streamSettings": {
        "sockopt": {
          "tproxy": "tproxy",
          "mark": 255
        }
      }
    }
  ],
  "outbounds": [
    {
      "tag": "direct",
      "protocol": "freedom",
      "settings": {
        "ipsBlocked": [] // 显式关闭默认私有 IP 拦截
      },
      "streamSettings": {
        "sockopt": {
          "domainStrategy": "UseIPv4",
          "mark": 255
        }
      }
    },
    {
      "tag": "block",
      "protocol": "blackhole",
      "settings": { "response": { "type": "http" } }
    },
    {
      "tag": "dns-out",
      "protocol": "dns",
      "settings": { "nonIPQuery": "reject" }, // Xray新增，可选 skip/drop/reject
      "streamSettings": { "sockopt": { "mark": 255 } }
    },
    // ---- VLESS + REALITY 出口示例 ----
    {
      "tag": "proxy-us:[vless]us-example-01-p443", // tag格式: proxy-{区域}:[协议]{节点名}-p{端口}
      "protocol": "vless",
      "settings": {
        "vnext": [{
          "address": "your-us-server.example.com",
          "port": 443,
          "users": [{
            "id": "your-uuid-here",
            "encryption": "none",
            "flow": "xtls-rprx-vision"
          }]
        }]
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "fingerprint": "chrome",
          "serverName": "your-sni.example.com",
          "publicKey": "your-public-key",
          "spiderX": "/",
          "shortId": "your-short-id"
        },
        "sockopt": { "domainStrategy": "UseIP", "mark": 255 }
      },
      "mux": {
        "enabled": false, // ⚠️ VLESS+REALITY 不支持 mux，必须保持关闭
        "concurrency": 2,
        "xudpConcurrency": 2,       // Xray新增，XUDP复用并发数
        "xudpProxyUDP443": "reject"  // Xray新增，XUDP对UDP443的处理策略
      }
    },
    // 同区域可添加更多节点，tag 前缀保持一致即可被 selector 匹配
    // { "tag": "proxy-us:[vless]us-example-02-p443", ... },
    // { "tag": "proxy-us:[hy2]us-hy2-01-p8443", ... },

    // ---- Hysteria2 出口示例 ----
    {
      "tag": "proxy-tw:[hy2]tw-example-01-p8443",
      "protocol": "hysteria",
      "settings": {
        "version": 2,
        "address": "your-tw-server.example.com",
        "port": 8443
      },
      "streamSettings": {
        "network": "hysteria",
        "security": "tls",
        "tlsSettings": {
          "serverName": "your-tw-server.example.com",
          "allowInsecure": false,
          "alpn": ["h3"]
        },
        "hysteriaSettings": {
          "version": 2,
          "auth": "your-hy2-auth-password"
        },
        "sockopt": { "domainStrategy": "UseIP", "mark": 255 }
      },
      "mux": {
        "enabled": false, // ⚠️ Hysteria2 不支持 mux，必须保持关闭
        "concurrency": 2,
        "xudpConcurrency": -1,
        "xudpProxyUDP443": "skip"
      }
    }
    // 同理可添加 proxy-jp:、proxy-sg:、proxy-hk: 等区域的节点
  ],
  "dns": {
    "queryStrategy": "UseIPv4",
    "disableFallbackIfMatch": true, // Xray新增，替代V2Ray的 fallbackStrategy: "disabled-if-any-match"
    "serveExpiredTTL": 90,          // Xray新增，DNS过期后仍可在此秒数内返回缓存
    "enableParallelQuery": true,    // Xray新增，并行查询加速DNS解析
    "hosts": {
      "geosite:category-ads-all": "127.0.0.1"
    },
    "servers": [
      {
        // 默认国内DNS，只接受大陆IP结果
        "address": "your-isp-dns-ip",
        "port": 53,
        "expectIPs": ["geoip:cn"],
        "tag": "dns-china-try-resolve"
      },
      {
        "address": "119.29.29.29",
        "port": 53,
        "expectIPs": ["geoip:cn"],
        "tag": "dns-china-try-resolve-backup"
      },
      {
        // 海外默认DNS兜底
        "address": "1.1.1.1",
        "port": 53,
        "tag": "dns-default-abroad"
      },
      {
        // JP相关域名走Cloudflare DNS
        "address": "1.1.1.1",
        "port": 53,
        "domains": [
          "geosite:anthropic",
          "geosite:openai",
          "geosite:github",
          "geosite:pixiv",
          "regexp:.*\\.jp$"
        ],
        "skipFallback": true, // Xray新增，命中此规则后不再fallback到其他DNS
        "finalQuery": true,  // Xray新增，标记为最终查询，不再尝试后续DNS
        "tag": "dns-jp-site"
      },
      {
        // ⚠️ 代理节点域名必须走国内DNS直连解析，否则会导致环路
        "address": "114.114.114.114",
        "port": 53,
        "domains": [
          "domain:synology.com",
          "domain:synology.cn",
          "full:your-us-server.example.com",  // Auto-Generated DIRECT-DOMAIN
          "full:your-tw-server.example.com"   // Auto-Generated DIRECT-DOMAIN
          // ... 所有代理节点域名都需要加在这里
        ],
        "skipFallback": true,
        "tag": "dns-china-special"
      },
      {
        // 大陆域名白名单
        "address": "your-isp-dns-ip",
        "port": 53,
        "domains": [
          "domain:ntp.org",
          "domain:steamserver.net",
          "geosite:mihoyo-cn",
          "geosite:china-list",
          "geosite:apple",
          "geosite:apple-cn",
          "geosite:icloud",
          "geosite:category-games@cn",
          "geosite:geolocation-cn"
        ],
        "skipFallback": true,
        "tag": "dns-china-site"
      },
      {
        "address": "119.29.29.29",
        "port": 53,
        "domains": [
          "domain:ntp.org",
          "domain:steamserver.net",
          "geosite:mihoyo-cn",
          "geosite:china-list",
          "geosite:apple",
          "geosite:apple-cn",
          "geosite:icloud",
          "geosite:category-games@cn",
          "geosite:geolocation-cn"
        ],
        "skipFallback": true,
        "tag": "dns-china-site-backup"
      },
      {
        "address": "1.1.1.1",
        "port": 53,
        "domains": [
          "geosite:facebook",
          "geosite:twitter",
          "geosite:netflix",
          "geosite:tiktok"
        ],
        "skipFallback": true,
        "finalQuery": true,
        "tag": "dns-usa-site"
      },
      {
        "address": "8.8.8.8",
        "port": 53,
        "domains": [
          "geosite:google",
          "geosite:google-play",
          "geosite:google-gemini"
        ],
        "skipFallback": true,
        "finalQuery": true,
        "tag": "dns-jp-special"
      }
    ]
  },
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "balancers": [
      {
        "tag": "balancer-proxy-default",
        "selector": ["proxy-us:", "proxy-tw:", "proxy-hk:"],
        "strategy": {
          "type": "leastload",
          "settings": {
            "observerTag": "observatory-internet-default",
            "expected": 2,
            "maxRTT": "3s",
            "tolerance": 0.1,
            "baselines": ["100ms", "300ms", "600ms", "1s"],
            "costs": [{"match": "电信", "value": 0.7}]
          }
        },
        "fallbackTag": "direct"
      },
      {
        "tag": "balancer-proxy-jp",
        "selector": ["proxy-jp:", "proxy-sg:"],
        "strategy": {
          "type": "leastload",
          "settings": {
            "observerTag": "observatory-internet-jp",
            "expected": 2,
            "maxRTT": "3s",
            "tolerance": 0.1,
            "baselines": ["100ms", "300ms", "600ms", "1s"],
            "costs": [{"match": "电信", "value": 0.7}]
          }
        },
        "fallbackTag": "direct"
      }
    ],
    "rules": [
      { // stats api
        "type": "field",
        "inboundTag": ["api"],
        "outboundTag": "api"
      },
      { // 广告拦截
        "type": "field",
        "domain": ["geosite:category-ads-all"],
        "outboundTag": "block"
      },
      {
        // ⚠️ 重要：代理服务器域名必须直连，否则会导致路由环路
        "type": "field",
        "domain": [
          "full:your-us-server.example.com",  // Auto-Generated DIRECT-DOMAIN
          "full:your-tw-server.example.com"   // Auto-Generated DIRECT-DOMAIN
          // ... 所有代理节点域名都需要加在这里，和 dns-china-special 保持一致
        ],
        "outboundTag": "direct"
      },
      { // BT 流量直连
        "type": "field",
        "protocol": ["bittorrent"],
        "outboundTag": "direct"
      },
      { // NTP 直连
        "type": "field",
        "inboundTag": ["transparent"],
        "port": 123,
        "network": "udp",
        "outboundTag": "direct"
      },
      { // 劫持 DNS 到 Xray 内置 DNS
        "type": "field",
        "inboundTag": ["transparent"],
        "port": 53,
        "network": "udp",
        "outboundTag": "dns-out"
      },
      { // 国内DNS流量直连
        "type": "field",
        "inboundTag": [
          "dns-china-try-resolve",
          "dns-china-try-resolve-backup",
          "dns-china-special",
          "dns-china-site",
          "dns-china-site-backup"
        ],
        "outboundTag": "direct"
      },
      { // JP DNS 流量走 JP 负载均衡
        "type": "field",
        "inboundTag": ["dns-jp-special", "dns-jp-site"],
        "balancerTag": "balancer-proxy-jp"
      },
      { // USA DNS 流量走默认负载均衡
        "type": "field",
        "inboundTag": ["dns-usa-site"],
        "balancerTag": "balancer-proxy-default"
      },
      { // 海外默认 DNS 流量走默认负载均衡
        "type": "field",
        "inboundTag": ["dns-default-abroad"],
        "balancerTag": "balancer-proxy-default"
      },
      { // 直连 本地保留 IP
        "type": "field",
        "ip": ["geoip:private"],
        "outboundTag": "direct"
      },
      { // 直连 国内网站
        "type": "field",
        "domain": [
          "domain:ntp.org",
          "domain:steamserver.net",
          "geosite:mihoyo-cn",
          "geosite:china-list",
          "geosite:apple",
          "geosite:apple-cn",
          "geosite:icloud",
          "geosite:category-games@cn",
          "geosite:geolocation-cn"
        ],
        "outboundTag": "direct"
      },
      { // 直连 国内IP
        "type": "field",
        "ip": ["geoip:cn"],
        "outboundTag": "direct"
      },
      { // Google/Pixiv/OpenAI/Anthropic 等走 JP 出口
        "type": "field",
        "domain": [
          "geosite:google",
          "geosite:google-play",
          "geosite:google-gemini",
          "geosite:pixiv",
          "geosite:openai",
          "geosite:anthropic",
          "geosite:github",
          "regexp:.*\\.jp$"
        ],
        "balancerTag": "balancer-proxy-jp"
      },
      { // JP IP
        "type": "field",
        "ip": ["geoip:google", "geoip:jp"],
        "balancerTag": "balancer-proxy-jp"
      },
      { // conn-track: JP 出口
        "type": "field",
        "source": "dynamic-ipset:dnscircuit-conntrack-src-balancer-proxy-jp",
        "ip": "dynamic-ipset:dnscircuit-conntrack-dest-balancer-proxy-jp",
        "balancerTag": "balancer-proxy-jp",
        "outboundTag": ""
      },
      { // Twitter/Netflix/TikTok 走默认负载均衡
        "type": "field",
        "domain": [
          "geosite:facebook",
          "geosite:twitter",
          "geosite:netflix",
          "geosite:tiktok"
        ],
        "balancerTag": "balancer-proxy-default"
      },
      { // Telegram 走默认负载均衡
        "type": "field",
        "ip": ["geoip:telegram"],
        "balancerTag": "balancer-proxy-default"
      },
      { // conn-track: 默认出口
        "type": "field",
        "source": "dynamic-ipset:dnscircuit-conntrack-src-balancer-proxy-default",
        "ip": "dynamic-ipset:dnscircuit-conntrack-dest-balancer-proxy-default",
        "balancerTag": "balancer-proxy-default",
        "outboundTag": ""
      },
      { // dnsCircuit 默认路由兜底
        "type": "field",
        "ip": "dynamic-ipset:dnscircuit-dest-default",
        "balancerTag": "balancer-proxy-default"
      },
      { // 兜底：所有未匹配流量走默认负载均衡
        "type": "field",
        "network": "tcp,udp",
        "balancerTag": "balancer-proxy-default"
      }
    ]
  },
  // ⚠️ 本项目核心配置，不要遗漏。详细字段说明见上文"配置Xray的OSPF模块"章节
  "dnsCircuit": {
    "dnsOutboundTag": "dns-out",
    "inboundTags": ["transparent"],
    "balancerTags": ["balancer-proxy-default", "balancer-proxy-jp"],
    "persistentRoute": [
      "geoip:telegram",
      "10.0.0.0/8",
      "172.16.0.0/12"
    ],
    "inactiveClean": 172800,
    "ospfSetting": {
      "ifName": "{IFNAME}",
      "address": "192.168.66.2/24"
    }
  }
}
```

**与V2Ray版本的关键配置差异总结：**

| 配置项 | V2Ray | Xray |
|-------|-------|------|
| 推荐协议 | VMess | VLESS + REALITY |
| freedom outbound 私有IP拦截 | 无此配置 | `settings.ipsBlocked: []` 显式关闭 |
| DNS fallback策略 | `fallbackStrategy: "disabled-if-any-match"` | `disableFallbackIfMatch: true` |
| DNS过期缓存 | 不支持 | `serveExpiredTTL: 90` |
| DNS并行查询 | 不支持 | `enableParallelQuery: true` |
| DNS跳过fallback | 不支持 | `skipFallback: true` |
| DNS最终查询 | 不支持 | `finalQuery: true` |
| 连接观测器类型 | `default`（简单HTTP探测） | `burst`（持续探测+详细统计：RTT均值/标准差/失败率） |
| 观测器探测配置 | `probeURL` + `probeInterval` | `pingConfig`（含 `destination`/`interval`/`sampling`/`timeout`/`httpMethod`） |
| 负载均衡策略 | `leastping`（仅看延迟） | `leastload`（综合延迟+稳定性+失败率，支持 `baselines`/`costs`/`tolerance`） |
| 负载均衡可用策略 | `random`/`leastping` | `random`/`leastping`/`roundrobin`/`leastload` |
| inactiveClean 默认值 | 21600秒（6小时） | 86400秒（24小时） |
| MUX配置 | 基础mux | 支持 `xudpConcurrency`/`xudpProxyUDP443` |
| API服务 | `StatsService` | `StatsService` + `ObservatoryService` |

# FAQs

## OSPF 收敛速度快吗？

很快，基本在DNS请求发出后的1秒内就可以完成路由收敛，体感首次访问某个被墙站点时，有30-40%的概率会出现ConnectionRST，
随后只需要刷新一下页面即可正常访问。

同时，由于默认路由有效期可配置（默认24小时，示例中设置为48小时），对于常用网站，只要在有效期内访问过，对应路由规则就会一直生效。

## 这个修改版的Xray为什么关闭有点慢

因为使用了OSPF协议，其标准要求，在路由下线时，必须从广播域中废止自己生成的路由条目。
所以，在收到退出信号时，旁路由广播废止路由表后，其实在等待主路由对于废止条目的确认，这个一般需要1-2秒。


# Stargazers over time
[![Stargazers over time](https://starchart.cc/Einsfier/Xray-core.svg)](https://starchart.cc/Einsfier/Xray-core)





