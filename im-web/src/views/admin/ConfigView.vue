<template>
  <div class="config-page">
    <!-- 左侧分区导航 -->
    <aside class="config-nav">
      <button
        v-for="s in sections"
        :key="s.key"
        class="nav-btn"
        :class="{ active: activeSection === s.key }"
        @click="activeSection = s.key"
      >
        <component :is="s.icon" />
        <span>{{ s.title }}</span>
      </button>
    </aside>

    <!-- 右侧内容 -->
    <div class="config-body">
      <!-- 品牌 -->
      <div v-show="activeSection === 'brand'" class="section">
        <h2 class="section-title">品牌设置</h2>
        <p class="section-desc">登录/注册页 Logo 与名称，客户端从 /auth/config 读取</p>

        <a-card class="form-card brand-card">
          <a-form layout="vertical" :label-col-props="{ span: 24 }">
            <a-form-item label="应用名称">
              <a-input v-model="brand.appName" placeholder="如：ChatPulse" />
            </a-form-item>
            <a-form-item label="品牌名称">
              <a-input v-model="brand.brandName" placeholder="如：ChatPulse" />
            </a-form-item>
            <a-form-item label="应用 Logo">
              <ImageUpload v-model="brand.appLogo" dir="brand/" :inline="true" :size="96" hint="建议尺寸 128×128，PNG/SVG。登录页 / 关于页左上角展示。" />
            </a-form-item>
            <a-form-item label="品牌 Logo（可选）">
              <ImageUpload v-model="brand.brandLogo" dir="brand/" :inline="true" :size="96" hint="用于品牌展示位。留空则使用应用 Logo。" />
            </a-form-item>
            <a-form-item label="默认头像（新注册用户使用）">
              <ImageUpload v-model="misc.defaultAvatar" dir="avatar/" round :inline="true" :size="96" hint="圆形预览，留空则使用系统默认头像。" />
            </a-form-item>
            <a-form-item label="位置消息地图引擎">
              <a-radio-group v-model="misc.mapEngine">
                <a-radio value="google">谷歌地图</a-radio>
                <a-radio value="amap">高德地图</a-radio>
              </a-radio-group>
            </a-form-item>
            <a-form-item label="谷歌地图 API Key（引擎 = 谷歌时使用）">
              <SecretInput v-model="misc.googleMapsKey" placeholder="AIza..." allow-clear />
            </a-form-item>
            <a-form-item label="高德 Web端(JS API) Key（引擎 = 高德时使用）">
              <SecretInput v-model="misc.amapJsKey" placeholder="高德控制台 Key 类型必须选「Web端(JS API)」" allow-clear />
            </a-form-item>
            <a-form-item label="高德安全密钥 securityJsCode">
              <SecretInput v-model="misc.amapJscode" allow-clear />
            </a-form-item>
            <a-form-item label="高德 Web服务 Key（气泡静态图）">
              <SecretInput v-model="misc.amapWebKey" placeholder="Key 类型必须选「Web服务」" allow-clear />
            </a-form-item>
            <div class="form-actions">
              <a-button type="primary" @click="saveBrand">保存品牌设置</a-button>
            </div>
          </a-form>
        </a-card>
      </div>

      <!-- 注册与认证 -->
      <div v-show="activeSection === 'auth'" class="section">
        <h2 class="section-title">注册与认证</h2>
        <p class="section-desc">控制账号注册与登录认证方式</p>

        <a-card class="form-card">
          <a-form layout="vertical" :model="cfg">
            <a-form-item label="开放注册">
              <a-switch v-model="cfg.registerOn" @change="save('register_enabled', $event)" />
            </a-form-item>
            <a-form-item label="注册方式">
              <a-radio-group v-model="cfg.registerType" @change="save('register_type', $event)">
                <a-radio value="account">账号密码</a-radio>
                <a-radio value="phone">手机号</a-radio>
                <a-radio value="email">邮箱</a-radio>
              </a-radio-group>
              <template #extra>
                注册方式 = 手机号：前端注册填手机号（默认 +86）；= 邮箱：前端注册必须填邮箱；= 账号密码：保持现有用户名/邮箱/手机号均可
              </template>
            </a-form-item>
            <a-form-item label="开启注册认证">
              <a-switch v-model="cfg.registerVerifyOn" @change="save('register_verify_enabled', $event)" />
              <template #extra>
                开启后：手机号注册必须短信验证码认证、邮箱注册必须邮箱验证码认证；账号密码注册方式不受影响，始终无需验证码
              </template>
            </a-form-item>
            <a-form-item label="邀请码注册">
              <a-switch v-model="cfg.inviteCodeOn" @change="save('invite_code_enabled', $event)" />
              <template #extra>
                开启后：前端注册页必填邀请码并显示输入框，游客注册弹出邀请码，用户中心显示「我的邀请码」；关闭后注册页无邀请码输入框，游客直接进入，用户中心不显示邀请码
              </template>
            </a-form-item>
            <a-form-item label="游客注册">
              <a-switch v-model="cfg.guestOn" @change="save('guest_register_enabled', $event)" />
              <template #extra>
                开启后：App 未登录用户首屏进入「游客模式」引导页，可一键按设备号自动注册并登录；若同时开启「邀请码注册」，游客登录后会引导填写邀请码并自动加好友
              </template>
            </a-form-item>
            <a-form-item label="图形验证码">
              <a-switch v-model="cfg.captchaOn" @change="save('captcha_enabled', $event)" />
            </a-form-item>
            <a-form-item label="消息加密方式">
              <a-radio-group v-model="cfg.e2eeMode" @change="save('e2ee_mode', $event)">
                <a-radio value="off">不加密</a-radio>
                <a-radio value="server">服务端加密（服务端可解密）</a-radio>
                <a-radio value="e2ee">端到端加密（服务端无法解密）</a-radio>
              </a-radio-group>
              <template #extra>
                端到端加密：私钥仅存用户设备、服务端只存密文；用户可在「账号与安全」中自行关闭（默认开启）。切换为端到端加密后，已登录用户下次进入聊天时自动生成密钥；后台重置用户密码会同时销毁其私钥备份，该用户的加密历史消息将无法解密
              </template>
            </a-form-item>
          </a-form>
        </a-card>
      </div>

      <!-- 功能开关 -->
      <div v-show="activeSection === 'feature'" class="section">
        <h2 class="section-title">功能开关</h2>
        <p class="section-desc">控制客户端功能入口的显示，App 端从 /auth/config 实时读取</p>

        <a-card class="form-card">
          <a-form layout="vertical">
            <a-form-item label="开启零钱">
              <a-switch v-model="feature.walletOn" @change="save('wallet_enabled', $event)" />
              <template #extra>
                关闭后：聊天窗口不显示红包/转账入口，用户中心不显示「我的钱包」
              </template>
            </a-form-item>
            <a-form-item label="开启小助手">
              <a-switch v-model="feature.assistantOn" @change="save('assistant_enabled', $event)" />
              <template #extra>
                关闭后：新注册用户（含游客）不自动添加小助手，通讯录不显示小助手入口；已添加的存量用户不受影响
              </template>
            </a-form-item>
            <a-form-item label="开启频道">
              <a-switch v-model="feature.channelOn" @change="save('channel_enabled', $event)" />
              <template #extra>
                关闭后：App 隐藏「新建频道」入口，注册不自动关注频道；旧版本 App 创建频道将被服务端拒绝
              </template>
            </a-form-item>
            <a-form-item label="账户安全验证">
              <a-switch v-model="feature.accountVerifyOn" @change="save('account_verify_enabled', $event)" />
              <template #extra>
                开启后新设备登录需二次验证：PC 收小助手验证码（关闭小助手则弹设备批准窗）、App 需最近可信设备批准；小助手开启时每次登录均推送登录提醒。手动注销的设备下次登录须重新批准
              </template>
            </a-form-item>
          </a-form>
        </a-card>
      </div>

      <!-- 聊天设置（客服设置 + 注册进群 + 默认关注频道 + 群聊设置，Tab 分页 + 底部统一保存） -->
      <div v-show="activeSection === 'chat'" class="section chat-section">
        <h2 class="section-title">聊天设置</h2>
        <p class="section-desc">客服分配、注册自动进群/关注频道与群人数上限，改完点底部「保存聊天设置」一次生效</p>

        <a-card class="form-card">
          <a-tabs default-active-key="kefu" type="rounded">
            <!-- Tab 1：客服设置（key=kefu_config） -->
            <a-tab-pane key="kefu" title="客服设置">
              <a-form layout="vertical" :model="kefu">
                <a-form-item label="注册自动添加客服">
                  <a-switch v-model="kefu.autoAdd" />
                </a-form-item>
                <a-form-item label="添加方式">
                  <a-radio-group v-model="kefu.mode">
                    <a-radio value="round">轮流（每位新用户分配一位客服）</a-radio>
                    <a-radio value="all">全部（添加所有客服）</a-radio>
                  </a-radio-group>
                </a-form-item>
                <a-form-item label="客服自动打招呼内容">
                  <a-textarea v-model="kefu.greeting" :rows="3" :max-length="200" show-word-limit
                              placeholder="例如：你好，我是 {nickname}，很高兴为您服务~（{nickname} 会被替换成客服昵称）" />
                  <div style="color: #86909c; font-size: 12px; margin-top: 4px">
                    留空则不发送；先在「用户管理」把用户角色设为「客服」，新注册用户才会按配置添加客服。
                  </div>
                </a-form-item>
              </a-form>
            </a-tab-pane>

            <!-- Tab 2：注册进群（key=default_group_config） -->
            <a-tab-pane key="group" title="注册进群">
              <a-form layout="vertical" :model="defaultGroup">
                <a-form-item label="注册自动进群">
                  <a-switch v-model="defaultGroup.enabled" />
                </a-form-item>
                <a-form-item label="默认加入的群聊（可多选）">
                  <a-select v-model="defaultGroup.groupIds" multiple :options="groupOptions" placeholder="选择群聊"
                            :loading="groupOptionsLoading" allow-clear />
                  <div style="color: #86909c; font-size: 12px; margin-top: 4px">
                    群列表来自后台「群组管理」（最多最近 200 个群）。
                  </div>
                </a-form-item>
              </a-form>
            </a-tab-pane>

            <!-- Tab 3：默认关注频道（key=default_channel_config） -->
            <a-tab-pane key="channel" title="默认关注频道">
              <a-form layout="vertical" :model="defaultChannel">
                <a-form-item label="注册自动关注频道">
                  <a-switch v-model="defaultChannel.enabled" />
                </a-form-item>
                <a-form-item label="默认关注的频道（可多选）">
                  <a-select v-model="defaultChannel.channelIds" multiple :options="channelOptions" placeholder="选择频道"
                            :loading="channelOptionsLoading" allow-clear />
                  <div style="color: #86909c; font-size: 12px; margin-top: 4px">
                    频道 ID 以字符串存取（雪花 ID 超出 JS 安全整数范围，数字会精度丢失）。
                  </div>
                  <div v-if="channelOptionsError" style="color: #f53f3f; font-size: 12px; margin-top: 4px">
                    频道列表接口暂不可用（需后端提供 /admin/conversations?type=3），下拉暂为空，可稍后重试。
                  </div>
                </a-form-item>
                <a-form-item label="欢迎语模板">
                  <a-textarea v-model="defaultChannel.welcomeMsg" :auto-size="{ minRows: 2, maxRows: 3 }"
                              placeholder="欢迎 {nickname} 关注 {channelName}！留空使用默认欢迎语" allow-clear />
                  <div style="color: #86909c; font-size: 12px; margin-top: 4px">
                    可用占位符：{nickname}/{订阅人昵称} 订阅用户昵称、{channelName}/{频道名} 频道名（中英文写法等效）；留空时后端使用默认欢迎语「欢迎关注「频道名」」。所有关注入口（注册自动关注、手动关注、频道主邀请）均会发送
                  </div>
                </a-form-item>
              </a-form>
            </a-tab-pane>

            <!-- Tab 4：群聊设置（key=group_max_members） -->
            <a-tab-pane key="groupMax" title="群聊设置">
              <a-form layout="vertical" :model="groupMax">
                <a-form-item label="群聊人数上限">
                  <a-input-number v-model="groupMax" :min="0" :precision="0" style="width: 200px" />
                  <template #extra>
                    0 表示不限制；仅对新建的群生效，已建群的各自上限不变
                  </template>
                </a-form-item>
              </a-form>
            </a-tab-pane>
          </a-tabs>

          <!-- 统一保存：四个子块一次写回，不再每张卡片一个保存按钮 -->
          <div class="chat-save-bar">
            <a-button type="primary" :loading="savingChat" @click="saveChatAll">保存聊天设置</a-button>
          </div>
        </a-card>
      </div>

      <!-- App 版本 -->
      <div v-show="activeSection === 'version'" class="section">
        <h2 class="section-title">App 版本与更新</h2>
        <p class="section-desc">客户端关于页 / 更新检查：安卓、iOS、PC 三端独立检测，H5 不检测</p>

        <a-card class="form-card">
          <a-form layout="vertical">
            <a-form-item label="安卓版本号">
              <a-input v-model="version.androidVersion" placeholder="1.0.0" />
              <template #extra>安卓客户端与该版本号不一致时提示更新；留空回退旧键「版本号」</template>
            </a-form-item>
            <a-form-item label="iOS 版本号">
              <a-input v-model="version.iosVersion" placeholder="1.0.0" />
              <template #extra>iOS 客户端与该版本号不一致时提示更新；留空回退旧键「版本号」</template>
            </a-form-item>
            <a-form-item label="PC 版本号">
              <a-input v-model="version.pcVersion" placeholder="7.9.1" />
              <template #extra>PC 客户端与该版本号不一致时提示更新</template>
            </a-form-item>
            <a-form-item label="更新内容（三端共用）">
              <a-textarea v-model="version.updateLog" :rows="3" placeholder="本次更新说明" />
            </a-form-item>
            <a-form-item label="Android 下载地址">
              <a-input v-model="version.androidUrl" placeholder="https://..." />
            </a-form-item>
            <a-form-item label="iOS 下载地址">
              <a-input v-model="version.iosUrl" placeholder="https://..." />
            </a-form-item>
            <a-form-item label="PC 下载地址">
              <a-input v-model="version.pcUrl" placeholder="https://..." />
              <template #extra>PC 检测到新版本时引导用户打开该地址下载</template>
            </a-form-item>
            <a-form-item label="热更新地址">
              <a-input v-model="version.hotUpdateUrl" placeholder="https://..." />
            </a-form-item>
            <a-form-item label="旧版基准版本号（兼容保留）">
              <a-input v-model="version.appVersion" placeholder="1.0.0" />
              <template #extra>仅作为安卓/iOS 版本号留空时的回退值，建议三个版本号都配置后忽略此项</template>
            </a-form-item>
            <a-button type="primary" @click="saveVersion">保存版本信息</a-button>
          </a-form>
        </a-card>
      </div>

      <!-- 通知 -->
      <div v-show="activeSection === 'notify'" class="section">
        <h2 class="section-title">系统公告</h2>
        <p class="section-desc">移动端消息页跑马灯横幅</p>

        <a-card class="form-card">
          <a-form layout="vertical">
            <a-form-item label="公告内容">
              <a-textarea v-model="announcement" :rows="4" placeholder="如：欢迎使用 ChatPulse! 请注意账号安全，不要泄露验证码。" />
            </a-form-item>
            <a-button type="primary" @click="saveAnnouncement">保存公告</a-button>
          </a-form>
        </a-card>
      </div>

      <!-- 存储 -->
      <div v-show="activeSection === 'storage'" class="section">
        <h2 class="section-title">对象存储</h2>
        <p class="section-desc">文件/图片上传存储；可切换 MinIO 自建存储或阿里云 OSS，保存后即时生效，无需重启</p>

        <a-card class="form-card" title="存储驱动">
          <a-form layout="vertical">
            <a-form-item label="当前驱动（storage_driver）">
              <a-radio-group v-model="storage.driver">
                <a-radio value="minio">MinIO（自建，零云费用）</a-radio>
                <a-radio value="oss">阿里云 OSS（按用量计费，免运维）</a-radio>
              </a-radio-group>
              <template #extra>
                切换驱动只影响**之后的新上传**；历史文件仍可正常访问（下载/预览会自动回落到原存储读取）。
                注意：文档转 PDF 预览（im-convert）目前只支持 MinIO，OSS 模式下仍可直接下载原文件。
              </template>
            </a-form-item>
          </a-form>
        </a-card>

        <a-card v-if="storage.driver === 'oss'" class="form-card" title="阿里云 OSS 配置">
          <a-form layout="vertical" :model="oss">
            <a-form-item label="Endpoint（不带 https://）">
              <a-input v-model="oss.endpoint" placeholder="oss-cn-hangzhou.aliyuncs.com" />
            </a-form-item>
            <a-form-item label="Bucket 名称">
              <a-input v-model="oss.bucket" placeholder="im-files" />
            </a-form-item>
            <a-form-item label="AccessKey ID">
              <SecretInput v-model="oss.accessKeyId" placeholder="LTAI..." />
            </a-form-item>
            <a-form-item label="AccessKey Secret">
              <SecretInput v-model="oss.accessKeySecret" placeholder="••••••" />
            </a-form-item>
            <a-form-item label="公开访问 URL（可选，绑 CDN / 自定义域名时填）">
              <a-input v-model="oss.publicUrl" placeholder="留空则默认 https://{bucket}.{endpoint}" />
            </a-form-item>
            <template #extra>Bucket 建议设为「公共读」，头像/图片直链才能在客户端直接展示；私有时走后端签名代理读取</template>
          </a-form>
        </a-card>

        <a-card v-else class="form-card" title="MinIO 配置">
          <a-form layout="vertical" :model="minio">
            <a-form-item label="Endpoint">
              <a-input v-model="minio.endpoint" placeholder="127.0.0.1:9000" />
            </a-form-item>
            <a-form-item label="公网访问 URL">
              <a-input v-model="minio.publicUrl" placeholder="http://localhost:9000" />
            </a-form-item>
            <a-form-item label="AccessKey">
              <SecretInput v-model="minio.accessKey" placeholder="minioadmin" />
            </a-form-item>
            <a-form-item label="SecretKey">
              <SecretInput v-model="minio.secretKey" placeholder="••••••" />
            </a-form-item>
            <a-form-item label="Bucket 名称">
              <a-input v-model="minio.bucket" placeholder="im-files" />
            </a-form-item>
          </a-form>
        </a-card>

        <a-button type="primary" @click="saveStorage">保存存储配置</a-button>
      </div>

      <!-- 短信 -->
      <div v-show="activeSection === 'sms'" class="section">
        <h2 class="section-title">阿里云短信</h2>
        <p class="section-desc">auth_mode=sms 时发送验证码</p>

        <a-card class="form-card">
          <a-form layout="vertical" :model="sms">
            <a-form-item label="AccessKey ID">
              <SecretInput v-model="sms.accessKey" placeholder="LTAI..." />
            </a-form-item>
            <a-form-item label="AccessKey Secret">
              <SecretInput v-model="sms.secret" placeholder="••••••" />
            </a-form-item>
            <a-form-item label="短信签名">
              <a-input v-model="sms.signName" placeholder="ChatPulse" />
            </a-form-item>
            <a-form-item label="模板 Code">
              <a-input v-model="sms.templateCode" placeholder="SMS_123456789" />
            </a-form-item>
            <a-button type="primary" @click="saveSms">保存短信配置</a-button>
          </a-form>
        </a-card>
      </div>

      <!-- 邮件 SMTP -->
      <div v-show="activeSection === 'smtp'" class="section">
        <h2 class="section-title">邮件 SMTP</h2>
        <p class="section-desc">邮箱验证码（auth_mode=email）发信使用；保存后即时生效，无需重启。未配置时回退到环境变量 SMTP_*</p>

        <a-card class="form-card">
          <a-form layout="vertical" :model="smtp">
            <a-form-item label="SMTP 服务器（smtp_host）">
              <a-input v-model="smtp.host" placeholder="smtp.qq.com" />
            </a-form-item>
            <a-form-item label="发信账号（smtp_user）">
              <a-input v-model="smtp.user" placeholder="noreply@example.com" />
            </a-form-item>
            <a-form-item label="授权码 / 密码（smtp_password）">
              <SecretInput v-model="smtp.password" placeholder="••••••" />
            </a-form-item>
            <a-form-item label="发件人地址（smtp_from）">
              <a-input v-model="smtp.from" placeholder="noreply@example.com" />
            </a-form-item>
            <a-button type="primary" @click="saveSmtp">保存邮件配置</a-button>
          </a-form>
        </a-card>
      </div>

      <!-- 音视频 -->
      <div v-show="activeSection === 'trtc'" class="section">
        <h2 class="section-title">通话引擎</h2>
        <p class="section-desc">音视频通话（V2.0）。引擎切换后客户端下次发起通话生效；老版本客户端在 WebRTC 模式下会提示无法通话</p>

        <a-card class="form-card" title="引擎选择">
          <a-form layout="vertical">
            <a-form-item label="通话引擎">
              <a-radio-group v-model="trtc.engine" direction="vertical">
                <a-radio value="trtc">腾讯云 TRTC（按用量计费，打通率最高，推荐生产）</a-radio>
                <a-radio value="webrtc">开源 WebRTC（零云费用，需自建 STUN/TURN；群通话上限 4 人 Mesh）</a-radio>
                <a-radio value="livekit">开源 LiveKit（自托管 SFU，无云费用；SFU 转发不占终端带宽，群通话可扩）</a-radio>
              </a-radio-group>
            </a-form-item>
            <a-form-item v-if="trtc.engine === 'webrtc'" label="ICE 服务器（STUN/TURN，JSON 数组）">
              <a-textarea
                v-model="trtc.iceServers"
                :auto-size="{ minRows: 5, maxRows: 12 }"
                placeholder='[{"urls":"stun:stun.xxx.com:3478"},{"urls":"turn:xxx.com:3478","username":"turnuser","credential":"xxx"}]'
              />
              <template #extra>TURN 部署教程见仓库 im-server/doc/coturn_deploy.md；没有 TURN 时约 10-20% 对称 NAT 用户无法接通</template>
            </a-form-item>
          </a-form>
        </a-card>

        <a-card v-if="trtc.engine === 'livekit'" class="form-card" title="LiveKit（自托管 SFU）">
          <a-form layout="vertical">
            <a-form-item label="SFU 地址（WebSocket）">
              <a-input v-model="trtc.livekitUrl" placeholder="wss://livekit.xxx.com" />
              <template #extra>LiveKit 服务端对外地址；客户端（App/H5/PC）用它 + 下方签发的 JoinToken 直连房间</template>
            </a-form-item>
            <a-form-item label="API Key">
              <SecretInput v-model="trtc.livekitApiKey" placeholder="APIxxxxxxxx（livekit.yaml devkeys 或后台生成）" />
            </a-form-item>
            <a-form-item label="API Secret">
              <SecretInput v-model="trtc.livekitApiSecret" placeholder="••••••" />
            </a-form-item>
            <a-form-item label="部署参考">
              <template #extra>
                docker run --rm -p 7880:7880 -e LIVEKIT_KEYS="APIxxx: secretxxx" livekit/livekit-server --bind 0.0.0.0；
                生产建议 nginx 反代 wss（proxy_pass http://127.0.0.1:7880，Upgrade 头透传）后填 wss://域名
              </template>
            </a-form-item>
          </a-form>
        </a-card>

        <a-card v-if="trtc.engine === 'trtc'" class="form-card" title="腾讯云 TRTC">
          <a-form layout="vertical">
            <a-form-item label="SDKAppID">
              <SecretInput v-model="trtc.appId" placeholder="1400xxxxxx" />
            </a-form-item>
            <a-form-item label="SecretKey">
              <SecretInput v-model="trtc.secretKey" placeholder="••••••" />
            </a-form-item>
          </a-form>
        </a-card>
        <a-button type="primary" @click="saveTrtc">保存通话配置</a-button>
      </div>

      <!-- AI 翻译 -->
      <div v-show="activeSection === 'aiTranslate'" class="section">
        <h2 class="section-title">AI 翻译</h2>
        <p class="section-desc">聊天消息翻译（DeepSeek / 其他 OpenAI 兼容接口）。API Key 留空 = 关闭整个翻译功能；译文按「原文+目标语言」缓存，同一句话只消耗一次调用</p>

        <a-card class="form-card">
          <a-form layout="vertical" :model="aiTranslate">
            <a-form-item label="AI 翻译开关">
              <a-switch v-model="aiTranslate.enabled" checked-children="开" un-checked-children="关" />
              <template #extra>关闭后客户端消息气泡不显示「译」按钮，翻译接口整体停用</template>
            </a-form-item>
            <a-form-item label="接口地址（OpenAI 兼容 base_url）">
              <a-input v-model="aiTranslate.apiBase" placeholder="https://api.deepseek.com" />
            </a-form-item>
            <a-form-item label="API Key">
              <SecretInput v-model="aiTranslate.apiKey" placeholder="sk-••••••（留空 = 关闭 AI 翻译）" />
            </a-form-item>
            <a-form-item label="模型名">
              <a-input v-model="aiTranslate.model" placeholder="deepseek-chat" />
            </a-form-item>
            <a-form-item label="自动翻译每日次数（0 = 关闭自动翻译）">
              <a-input-number v-model="aiTranslate.autoDailyLimit" :min="0" :max="100000" style="width: 200px" />
            </a-form-item>
            <a-form-item label="手动翻译每日次数（0 = 不限量）">
              <a-input-number v-model="aiTranslate.manualDailyLimit" :min="0" :max="100000" style="width: 200px" />
            </a-form-item>
            <a-button type="primary" :loading="savingAiTranslate" @click="saveAiTranslate">保存 AI 翻译配置</a-button>
          </a-form>
        </a-card>
      </div>

      <!-- 推送（极光 / 个推） -->
      <div v-show="activeSection === 'jpush'" class="section">        <h2 class="section-title">推送（极光 / 个推）</h2>
        <p class="section-desc">App 离线消息推送：接收方不在线时，服务端按推送别名（=用户 ID）下发系统通知。iOS 服务商可切换（Android 固定走极光厂商通道）；切换后 1 分钟内生效</p>

        <a-card class="form-card" title="iOS 推送服务商">
          <a-form layout="vertical">
            <a-form-item label="iOS 端推送通道">
              <a-radio-group v-model="jpush.provider">
                <a-radio value="jpush">极光 JPush（默认）</a-radio>
                <a-radio value="getui">个推 Getui</a-radio>
              </a-radio-group>
              <template #extra>选「个推」后：有 iOS 设备的接收者走个推，其余仍走极光（Android 厂商通道）。已安装的 App 需重装/重开一次才会按新服务商注册推送</template>
            </a-form-item>
          </a-form>
        </a-card>

        <a-card class="form-card" title="极光 JPush" :class="{ 'form-card-dim': jpush.provider === 'getui' }">
          <a-form layout="vertical" :model="jpush">
            <a-form-item label="启用离线推送">
              <a-switch v-model="jpush.enabled" />
            </a-form-item>
            <a-form-item label="AppKey">
              <SecretInput v-model="jpush.appKey" placeholder="极光控制台的应用 AppKey" />
            </a-form-item>
            <a-form-item label="Master Secret">
              <SecretInput v-model="jpush.masterSecret" placeholder="••••••" />
            </a-form-item>
            <a-form-item label="iOS APNs 生产环境">
              <a-switch v-model="jpush.apnsProduction" />
              <template #extra>开发调试用开发环境（关）；正式上架后开启（开）。iOS 走个推时此开关不影响 iOS，仅约束 Android</template>
            </a-form-item>
          </a-form>
        </a-card>

        <a-card class="form-card" title="个推 Getui（iOS）" :class="{ 'form-card-dim': jpush.provider !== 'getui' }">
          <a-form layout="vertical" :model="jpush">
            <a-form-item label="启用个推">
              <a-switch v-model="jpush.getuiEnabled" />
              <template #extra>仅当上方 iOS 服务商选「个推」且此处开启时，iOS 离线推送才走个推</template>
            </a-form-item>
            <a-form-item label="App ID">
              <SecretInput v-model="jpush.getuiAppId" placeholder="个推控制台的应用 App ID" />
            </a-form-item>
            <a-form-item label="App Key">
              <SecretInput v-model="jpush.getuiAppKey" placeholder="个推控制台的 App Key" />
            </a-form-item>
            <a-form-item label="App Secret（iOS SDK 用）">
              <SecretInput v-model="jpush.getuiAppSecret" placeholder="••••••" />
              <template #extra>给 iOS 客户端 SDK 启动用（随 /auth/config 下发给 App），不是服务端鉴权用的那把</template>
            </a-form-item>
            <a-form-item label="MasterSecret（服务端鉴权用，必填）">
              <SecretInput v-model="jpush.getuiMasterSecret" placeholder="••••••" />
              <template #extra>服务端 REST v2 签名专用：sign = sha256(appkey+timestamp+mastersecret)。填错推送会报 20001 sign is invalid——注意与 App Secret 是两把不同的钥匙</template>
            </a-form-item>
            <template #extra>iOS 无需区分开发/生产环境：个推按设备上报的环境自动匹配证书（无极光 apns_production 的坑）</template>
          </a-form>
        </a-card>
        <a-button type="primary" @click="saveJpush">保存推送配置</a-button>
      </div>

      <!-- 节点 -->
      <div v-show="activeSection === 'infra'" class="section">
        <h2 class="section-title">节点服务器</h2>
        <p class="section-desc">多实例部署 / 负载均衡</p>

        <a-card class="form-card">
          <a-alert type="info" message="WS 节点列表在「节点管理」菜单编辑保存；JWT 签名秘钥由部署环境变量 JWT_SECRET 提供（deploy/.env），不要在后台配置——修改 .env 后需重启 api 容器" style="margin-bottom: 16px" />
          <a-form layout="vertical" :model="infra">
            <a-form-item label="当前节点 ID">
              <a-input v-model="infra.nodeId" placeholder="node-1（当前节点唯一标识）" />
            </a-form-item>
            <a-button type="primary" @click="saveInfra">保存节点配置</a-button>
          </a-form>
        </a-card>
      </div>

      <!-- 支付配置（在对象存储的下一块） -->
      <div v-show="activeSection === 'pay'" class="section pay-section">
        <h2 class="section-title">
          <span style="display:inline-flex;align-items:center;gap:8px">
            <IconQrcode style="font-size:20px;color:var(--color-primary-6)" />
            支付配置
          </span>
        </h2>
        <p class="section-desc">平台充值收款二维码、提现费率与门槛；客户端通过 <code>GET /api/v1/pay/config</code> 读取</p>

        <!-- 单卡片包住 Tab 内所有内容 -->
        <a-card class="pay-card" :bordered="false">
          <a-tabs default-active-key="channels" class="pay-tabs" :lazy-load="true">
            <!-- ================== Tab1：充值通道 ================== -->
            <a-tab-pane key="channels">
              <template #title>
                <span class="tab-title"><IconWechatpay style="color:#07c160" /> 充值通道</span>
              </template>

              <!-- 充值开关 头 -->
              <div class="pay-row head-row">
                <div>
                  <div class="row-title">充值通道开关</div>
                  <div class="row-desc">关闭后客户端「充值」页会显示「暂未开放充值」</div>
                </div>
                <a-switch v-model="pay.enabled" />
              </div>

              <!-- 收款码 3 列（内部分区，不再套 3 张独立 a-card） -->
              <div class="pay-divider">收款码</div>
              <div class="channels-grid">
                <div class="ch wechat">
                  <div class="ch-head">
                    <span class="ch-brand"><IconWechat /> 微信</span>
                    <a-tag v-if="pay.receiveWechatQrcodeUrl" color="green">已配置</a-tag>
                    <a-tag v-else color="gray">待上传</a-tag>
                  </div>
                  <div class="ch-main">
                    <ImageUpload
                      v-model="pay.receiveWechatQrcodeUrl"
                      dir="pay/qrcodes/"
                      :size="200"
                      hint="用户用微信扫码给平台打款"
                    />
                    <div class="ch-ops">
                      <a-button
                        type="outline"
                        size="small"
                        :disabled="!pay.receiveWechatQrcodeUrl"
                        @click="openQrPreview(pay.receiveWechatQrcodeUrl,'微信收款码')"
                      >
                        <template #icon><IconEye /></template>
                        查看大图
                      </a-button>
                      <a-popconfirm
                        :disabled="!pay.receiveWechatQrcodeUrl"
                        content="确定清除微信收款码？"
                        @ok="pay.receiveWechatQrcodeUrl = ''"
                      >
                        <a-button
                          size="small"
                          status="warning"
                          :disabled="!pay.receiveWechatQrcodeUrl"
                        >
                          <template #icon><IconDelete /></template>
                          清除
                        </a-button>
                      </a-popconfirm>
                    </div>
                  </div>
                </div>

                <div class="ch alipay">
                  <div class="ch-head">
                    <span class="ch-brand"><IconAlipayCircle /> 支付宝</span>
                    <a-tag v-if="pay.receiveAlipayQrcodeUrl" color="blue">已配置</a-tag>
                    <a-tag v-else color="gray">待上传</a-tag>
                  </div>
                  <div class="ch-main">
                    <ImageUpload
                      v-model="pay.receiveAlipayQrcodeUrl"
                      dir="pay/qrcodes/"
                      :size="200"
                      hint="用户用支付宝扫码给平台打款"
                    />
                    <div class="ch-ops">
                      <a-button
                        type="outline"
                        size="small"
                        :disabled="!pay.receiveAlipayQrcodeUrl"
                        @click="openQrPreview(pay.receiveAlipayQrcodeUrl,'支付宝收款码')"
                      >
                        <template #icon><IconEye /></template>
                        查看大图
                      </a-button>
                      <a-popconfirm
                        :disabled="!pay.receiveAlipayQrcodeUrl"
                        content="确定清除支付宝收款码？"
                        @ok="pay.receiveAlipayQrcodeUrl = ''"
                      >
                        <a-button
                          size="small"
                          status="warning"
                          :disabled="!pay.receiveAlipayQrcodeUrl"
                        >
                          <template #icon><IconDelete /></template>
                          清除
                        </a-button>
                      </a-popconfirm>
                    </div>
                  </div>
                </div>

                <div class="ch bank">
                  <div class="ch-head">
                    <span class="ch-brand"><IconExport /> 银行卡</span>
                    <a-tag v-if="pay.receiveBankQrcodeUrl || pay.receiveBankInfo.cardNo" color="orangered">已配置</a-tag>
                    <a-tag v-else color="gray">待上传</a-tag>
                  </div>
                  <div class="ch-main">
                    <ImageUpload
                      v-model="pay.receiveBankQrcodeUrl"
                      dir="pay/qrcodes/"
                      :size="200"
                      hint="银行卡转账二维码 / 收款截图"
                    />
                    <div class="ch-ops">
                      <a-button
                        type="outline"
                        size="small"
                        :disabled="!pay.receiveBankQrcodeUrl"
                        @click="openQrPreview(pay.receiveBankQrcodeUrl,'银行卡二维码/转账截图')"
                      >
                        <template #icon><IconEye /></template>
                        查看大图
                      </a-button>
                      <a-popconfirm
                        :disabled="!pay.receiveBankQrcodeUrl"
                        content="确定清除银行卡二维码图？"
                        @ok="pay.receiveBankQrcodeUrl = ''"
                      >
                        <a-button
                          size="small"
                          status="warning"
                          :disabled="!pay.receiveBankQrcodeUrl"
                        >
                          <template #icon><IconDelete /></template>
                          清除
                        </a-button>
                      </a-popconfirm>
                    </div>
                  </div>
                </div>
              </div>

              <!-- 银行卡文字信息（在同一张卡片内） -->
              <div class="pay-divider">银行卡信息 <span class="d-sub">（用户未扫收款码时展示文字）</span></div>
              <a-form layout="vertical" :label-col-props="{ span: 24 }">
                <a-row :gutter="16">
                  <a-col :xs="24" :sm="24" :md="8">
                    <a-form-item label="开户银行">
                      <a-input v-model="pay.receiveBankInfo.bankName" allow-clear placeholder="如：招商银行深圳南山支行" />
                    </a-form-item>
                  </a-col>
                  <a-col :xs="24" :sm="24" :md="8">
                    <a-form-item label="银行卡号">
                      <a-input v-model="pay.receiveBankInfo.cardNo" allow-clear placeholder="与持卡人一致的收款卡号" />
                    </a-form-item>
                  </a-col>
                  <a-col :xs="24" :sm="24" :md="8">
                    <a-form-item label="开户姓名">
                      <a-input v-model="pay.receiveBankInfo.accountName" allow-clear placeholder="持卡人真实姓名" />
                    </a-form-item>
                  </a-col>
                </a-row>
              </a-form>

              <!-- 充值提示（在同一张卡片内） -->
              <div class="pay-divider">充值提示 <span class="d-sub">（客户端充值页显示）</span></div>
              <a-textarea
                v-model="pay.rechargeTips"
                :rows="3" :max-length="240" show-word-limit
                placeholder="例：请扫码向平台支付对应金额，填写订单页上传支付凭证，1个工作日内审核通过后余额到账。"
              />
            </a-tab-pane>

            <!-- ================== Tab2：提现参数 ================== -->
            <a-tab-pane key="withdraw">
              <template #title>
                <span class="tab-title"><IconExport style="color:#ff7d00" /> 提现参数</span>
              </template>

              <!-- 启用开关 + Alert（在同一张卡片内） -->
              <div class="pay-row head-row">
                <div>
                  <div class="row-title">启用提现</div>
                  <div class="row-desc">关闭后客户端「提现」按钮灰掉，不允许提交新的提现申请</div>
                </div>
                <a-switch v-model="pay.withdrawEnabled" />
              </div>

              <a-alert type="info" :show-icon="true" class="pay-alert">
                <template #message>冻结与扣费规则</template>
                <template #description>
                  用户提交提现申请时：<b>先按 amount 冻结余额</b>。后台操作：
                  <span class="chip ok">确定提现</span> → frozen 解冻 amount + 余额扣 <code>fee</code>；
                  <span class="chip warn">驳回</span> → frozen amount <b>原路退回</b> 余额，不扣手续费。
                </template>
              </a-alert>

              <!-- 门槛 3 项 + 费率&预览 双栏（卡片内部） -->
              <div class="pay-divider">参数设置</div>
              <div class="withdraw-two-col">
                <!-- 左：门槛 -->
                <div class="col-block">
                  <div class="col-title">金额门槛</div>
                  <a-form layout="vertical" :label-col-props="{ span: 24 }">
                    <a-form-item label="单笔最低提现（元）">
                      <a-input-number
                        v-model="pay.withdrawMin" :min="0.01" :max="1000000" :precision="2" :step="10"
                        style="width:100%" hide-button prefix="¥"
                      />
                      <div class="hint">低于此值的提现申请会被拦截。</div>
                    </a-form-item>
                    <a-form-item label="单笔最高提现（元）">
                      <a-input-number
                        v-model="pay.withdrawMax" :min="0.01" :max="100000000" :precision="2" :step="1000"
                        style="width:100%" hide-button prefix="¥"
                      />
                      <div class="hint">防止一次提走大额余额。</div>
                    </a-form-item>
                    <a-form-item label="单笔最低手续费（元）">
                      <a-input-number
                        v-model="pay.withdrawFeeMin" :min="0" :max="10000" :precision="2" :step="1"
                        style="width:100%" hide-button prefix="¥"
                      />
                      <div class="hint">0 = 不设最低；一般 1 ~ 2 元。</div>
                    </a-form-item>
                  </a-form>
                </div>

                <!-- 右：费率 + 预览 -->
                <div class="col-block">
                  <div class="col-title">费率 & 实时预览</div>
                  <a-form layout="vertical" :label-col-props="{ span: 24 }">
                    <a-form-item label="手续费费率（0% ~ 10%）">
                      <div class="slider-row">
                        <a-slider
                          v-model="feeRateSlider"
                          :min="0" :max="100" :step="1"
                          :marks="sliderMarks"
                          style="flex:1"
                        />
                        <div class="rate-chip">
                          {{ feeRatePercent.toFixed(1) }}%
                        </div>
                      </div>
                    </a-form-item>
                  </a-form>

                  <div class="preview-box">
                    <div class="preview-title">示例：提现 ¥1,000 时</div>
                    <div class="preview-row">
                      <div>
                        <div class="label">手续费</div>
                        <div class="value fee">¥ {{ sampleFee.toFixed(2) }}</div>
                      </div>
                      <div>
                        <div class="label">到账金额</div>
                        <div class="value ok">¥ {{ (1000 - sampleFee).toFixed(2) }}</div>
                      </div>
                      <div>
                        <div class="label">冻结总额</div>
                        <div class="value">¥ 1,000.00</div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </a-tab-pane>
          </a-tabs>

          <!-- 底部操作条（在同一张卡片底部；sticky 贴在视口底部，长内容一滚就看见） -->
          <div class="pay-actions-inner">
            <a-space>
              <a-button type="outline" @click="loadPayConfig">重新加载</a-button>
              <a-button type="primary" status="success" :loading="paySaving" @click="savePayConfig">
                <template #icon><IconCheckCircle /></template>
                保存支付配置
              </a-button>
            </a-space>
          </div>
        </a-card>

        <!-- 二维码大图预览（section 层 Modal，不包在卡片里） -->
        <a-modal
          v-model:visible="qrVisible"
          :title="qrPreviewTitle"
          :footer="false"
          :mask-closable="true"
          width="520"
          @before-close="() => { qrPreviewSrc = '' }"
        >
          <div style="display:flex;justify-content:center">
            <img v-if="qrPreviewSrc" :src="qrPreviewSrc" style="max-width:100%;border-radius:12px;box-shadow:0 6px 20px rgba(0,0,0,.08)" />
          </div>
        </a-modal>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, markRaw } from 'vue'
import { Message } from '@arco-design/web-vue'
import { IconImage, IconUserGroup, IconInfoCircle, IconNotification, IconStorage, IconMessage, IconCamera, IconSettings, IconSend, IconQrcode, IconWechatpay, IconExport, IconEye, IconDelete, IconCheckCircle, IconExperiment, IconEmail, IconLanguage } from '@arco-design/web-vue/es/icon'
import { adminApi } from '@/api/admin'
import ImageUpload from './ImageUpload.vue'
import SecretInput from './SecretInput.vue'

const activeSection = ref('brand')

const sections = [
  { key: 'brand', title: '品牌', icon: markRaw(IconImage) },
  { key: 'feature', title: '功能开关', icon: markRaw(IconExperiment) },
  { key: 'chat', title: '聊天设置', icon: markRaw(IconMessage) },
  { key: 'auth', title: '注册认证', icon: markRaw(IconUserGroup) },
  { key: 'version', title: 'App 版本', icon: markRaw(IconInfoCircle) },
  { key: 'notify', title: '系统公告', icon: markRaw(IconNotification) },
  { key: 'storage', title: '对象存储', icon: markRaw(IconStorage) },
  { key: 'pay', title: '支付配置', icon: markRaw(IconQrcode) },
  { key: 'sms', title: '短信', icon: markRaw(IconMessage) },
  { key: 'smtp', title: '邮件', icon: markRaw(IconEmail) },
  { key: 'trtc', title: '音视频', icon: markRaw(IconCamera) },
  { key: 'aiTranslate', title: 'AI 翻译', icon: markRaw(IconLanguage) },
  { key: 'jpush', title: '推送', icon: markRaw(IconSend) },
  { key: 'infra', title: '节点', icon: markRaw(IconSettings) }
]

const cfg = ref({ registerOn: true, registerType: 'account' as string, registerVerifyOn: false, inviteCodeOn: false, captchaOn: false, e2eeMode: 'off' as string, guestOn: false })
  // 功能开关（默认开启：后台未配置时 sys_config 返回 null，视为开启）
  const feature = ref({ walletOn: true, assistantOn: true, channelOn: true, accountVerifyOn: false })
// 客服设置：kefu_config 为整体 JSON 键（{autoAdd, mode, greeting}），读取后解包渲染，保存时整体写回
const kefu = ref({ autoAdd: false, mode: 'round' as string, greeting: '' })
// 注册默认进群：default_group_config 整体 JSON 键（{enabled, groupIds}）
// 【铁律】群 ID 一律字符串存取：雪花 ID 超 JS 2^53，Number 化会丢精度
// （曾存出结尾被舍入的假 ID 353794126644248600 → 后端查群 not found）
const defaultGroup = ref({ enabled: false, groupIds: [] as string[] })
const groupOptions = ref<Array<{ label: string; value: string }>>([])
const groupOptionsLoading = ref(false)
// 默认关注频道：default_channel_config 整体 JSON 键（{enabled, channelIds}）
// 【铁律】同 groupIds：频道会话 ID 一律字符串存取，雪花 ID 超 JS 2^53，Number 化会丢精度
const defaultChannel = ref({ enabled: false, channelIds: [] as string[], welcomeMsg: '' })
const channelOptions = ref<Array<{ label: string; value: string }>>([])
const channelOptionsLoading = ref(false)
const channelOptionsError = ref(false)
// 群聊人数上限：group_max_members 独立标量键（0 = 不限制，仅对新建群生效）
const groupMax = ref<number>(0)
const savingGroupMax = ref(false)
// 聊天设置 Tab 统一保存
const savingChat = ref(false)
const brand = ref({ appName: '', brandName: '', appLogo: '', brandLogo: '' })
const version = ref({ appVersion: '', androidVersion: '', iosVersion: '', pcVersion: '', updateLog: '', androidUrl: '', iosUrl: '', pcUrl: '', hotUpdateUrl: '' })
const sms = ref({ accessKey: '', secret: '', signName: '', templateCode: '' })
const smtp = ref({ host: '', user: '', password: '', from: '' })
const trtc = ref({ appId: '', secretKey: '', engine: 'trtc', iceServers: '[]', livekitUrl: '', livekitApiKey: '', livekitApiSecret: '' })
// AI 翻译配置：ai_translate_* 独立标量键；限额 0 语义见模板说明（自动 0=关闭，手动 0=不限量）
const aiTranslate = ref({ enabled: true, apiBase: '', apiKey: '', model: '', autoDailyLimit: 50, manualDailyLimit: 0 })
const savingAiTranslate = ref(false)
const jpush = ref({
  provider: 'jpush',
  enabled: false,
  appKey: '',
  masterSecret: '',
  apnsProduction: false,
  getuiEnabled: false,
  getuiAppId: '',
  getuiAppKey: '',
  getuiAppSecret: '',
  getuiMasterSecret: ''
})
const minio = ref({ endpoint: '', publicUrl: '', accessKey: '', secretKey: '', bucket: '' })
// 对象存储驱动（minio / oss）+ 阿里云 OSS 配置（2026-09-25 需求）
const storage = ref<{ driver: 'minio' | 'oss' }>({ driver: 'minio' })
const oss = ref({ endpoint: '', bucket: '', accessKeyId: '', accessKeySecret: '', publicUrl: '' })
const infra = ref({ nodeId: '', jwtSecret: '' })
const announcement = ref('')
const misc = ref({ defaultAvatar: '', reservedIds: '', googleMapsKey: '', mapEngine: 'google', amapJsKey: '', amapJscode: '', amapWebKey: '' })

/**
 * 把后端返回的任意值安全转成 number：非法值（undefined / null / 非数字字符串 / NaN / Infinity）
 * 回退到 def，并夹取到 [min, max]。
 * 用途：阻止脏值进入表单 —— 一旦 withdrawFeeRate 变成 NaN，费率会显示 NaN%，
 * 且 Arco slider 的把手位置 left 会算成 NaN% 被渲染到轨道外，导致鼠标拖不动。
 */
function toNum(v: unknown, def = 0, min = -Infinity, max = Infinity): number {
  const n = typeof v === 'number' ? v : Number(v)
  if (!Number.isFinite(n)) return def
  return Math.min(Math.max(n, min), max)
}

// === 支付配置（放在对象存储下的 section） ===
const pay = ref({
  enabled: true,
  receiveWechatQrcodeUrl: '',
  receiveAlipayQrcodeUrl: '',
  receiveBankQrcodeUrl: '',
  receiveBankInfo: { bankName: '', cardNo: '', accountName: '' },
  rechargeTips: '',
  withdrawEnabled: true,
  withdrawMin: 10,
  withdrawMax: 50000,
  withdrawFeeRate: 0,
  withdrawFeeMin: 0
})
const paySaving = ref(false)
const qrVisible = ref(false)
const qrPreviewSrc = ref('')
const qrPreviewTitle = ref('')
// slider 用 0~100 整数（代表 0.0%~10.0%，每格 0.1%），避免浮点数把手定位丢失
const sliderMarks: Record<number, string> = {
  0: '0%', 10: '1%', 20: '2%', 50: '5%', 100: '10%'
}
// slider 绑定这个 computed：get 把小数转整数，set 把整数转回小数存入 pay.withdrawFeeRate
const feeRateSlider = computed({
  get: () => Math.round(toNum(pay.value.withdrawFeeRate, 0, 0, 0.1) * 1000),
  set: (v: number) => { pay.value.withdrawFeeRate = toNum(v / 1000, 0, 0, 0.1) }
})
// 费率百分比展示
const feeRatePercent = computed(() => toNum(pay.value.withdrawFeeRate, 0, 0, 0.1) * 100)

const sampleFee = computed(() => {
  const raw = 1000 * toNum(pay.value.withdrawFeeRate, 0, 0, 0.1)
  const min = toNum(pay.value.withdrawFeeMin, 0, 0)
  return Math.max(min, Math.round(raw * 100) / 100)
})

function openQrPreview(src: string, title: string) {
  if (!src) return
  qrPreviewSrc.value = src
  qrPreviewTitle.value = title
  qrVisible.value = true
}

async function loadPayConfig() {
  try {
    const { data } = await adminApi.payConfigGet()
    if (data?.code !== 0) { Message.error(data?.message || '读取支付配置失败'); return }
    const payload = (data?.data ?? {}) as Record<string, any>
    const { receiveBankInfo, ...rest } = payload
    Object.assign(pay.value, rest)
    // 数值字段强转 + 兜底 + 夹取：后端原样返回，运行时不做类型校验，
    // 脏值（undefined / null / 非数字字符串）会让费率显示 NaN% 并让滑块把手失效。
    // 只在字段确实存在时覆盖，缺失时沿用 pay 里的默认值。
    if ('withdrawFeeRate' in rest) pay.value.withdrawFeeRate = toNum(rest.withdrawFeeRate, 0, 0, 0.1)
    if ('withdrawFeeMin' in rest) pay.value.withdrawFeeMin = toNum(rest.withdrawFeeMin, 0, 0)
    if ('withdrawMin' in rest) pay.value.withdrawMin = toNum(rest.withdrawMin, 0, 0)
    if ('withdrawMax' in rest) pay.value.withdrawMax = toNum(rest.withdrawMax, 0, 0)
    if (receiveBankInfo && typeof receiveBankInfo === 'object') {
      Object.assign(pay.value.receiveBankInfo, receiveBankInfo)
    }
  } catch (e: any) {
    Message.error('读取支付配置失败：' + (e?.message || ''))
  }
}

async function savePayConfig() {
  // 校验
  if (pay.value.withdrawMin < 0.01) { Message.warning('提现最低金额必须 > 0'); return }
  if (pay.value.withdrawMax < pay.value.withdrawMin) { Message.warning('提现最高不能小于最低'); return }
  if (pay.value.withdrawFeeRate < 0 || pay.value.withdrawFeeRate > 0.1) { Message.warning('费率需在 0~0.1 之间'); return }
  if (pay.value.withdrawFeeMin < 0) { Message.warning('最低手续费不能为负'); return }
  paySaving.value = true
  try {
    const { enabled, receiveWechatQrcodeUrl, receiveAlipayQrcodeUrl, receiveBankQrcodeUrl, receiveBankInfo, rechargeTips, withdrawEnabled, withdrawMin, withdrawMax, withdrawFeeRate, withdrawFeeMin } = pay.value
    const payload = { enabled, receiveWechatQrcodeUrl, receiveAlipayQrcodeUrl, receiveBankQrcodeUrl, receiveBankInfo, rechargeTips, withdrawEnabled, withdrawMin, withdrawMax, withdrawFeeRate, withdrawFeeMin }
    const { data } = await adminApi.payConfigSet(payload)
    if (data?.code !== 0) { Message.error(data?.message || '保存失败'); return }
    Message.success('支付配置已保存')
    await loadPayConfig()
  } catch (e: any) {
    Message.error('保存支付配置失败：' + (e?.message || ''))
  } finally {
    paySaving.value = false
  }
}

onMounted(async () => {
  // 2026-09-22 需求6：auth_mode 由 register_type / register_verify_enabled 替代；
  // auth_mode 键保留只读（存量部署老客户端仍消费），后台不再提供编辑入口
  const flagKeys = ['register_enabled', 'register_type', 'register_verify_enabled', 'invite_code_enabled', 'captcha_enabled', 'e2e_enabled', 'guest_register_enabled']
  const [r, rt, rv, i, ca, e, g] = await Promise.all(flagKeys.map((k) => adminApi.configGet(k)))
  // register_type 未配置的存量部署：服务端按 auth_mode 推导（sms→phone/email→email），
  // 这里按同一规则回显，避免「服务端实际 phone+强制认证、后台显示 account+未开启」的误导
  const authModeRaw = String((await adminApi.configGet('auth_mode')).data.data || 'none')
  const legacyType = authModeRaw === 'sms' ? 'phone' : authModeRaw === 'email' ? 'email' : 'account'
  const legacyVerify = authModeRaw === 'sms' || authModeRaw === 'email'
  // 就地合并而非整体替换：以后新增配置字段即使漏写字面量，也不会把已有字段覆盖成 undefined
  Object.assign(cfg.value, {
    registerOn: !!r.data.data,
    registerType: rt.data.data === null || rt.data.data === '' ? legacyType : String(rt.data.data),
    registerVerifyOn: rt.data.data === null || rt.data.data === '' ? legacyVerify || !!rv.data.data : !!rv.data.data,
    inviteCodeOn: !!i.data.data,
    captchaOn: !!ca.data.data,
    // 后端 SysConfigGet("guest_register_enabled", false)：未配置即视为关闭，与这里默认 false 一致
    guestOn: !!g.data.data
  })
  // 加密方式（e2ee_mode: off/server/e2ee）。老部署只有 e2e_enabled 布尔键：
  // e2ee_mode 未配置时按其推导（true→server / false→off），与后端 E2eeMode() 兜底规则一致
  const em = await adminApi.configGet('e2ee_mode')
  const legacyOn = !!e.data.data
  const emVal = em.data.data
  cfg.value.e2eeMode = emVal === null || emVal === undefined || emVal === '' ? (legacyOn ? 'server' : 'off') : String(emVal)
    // 功能开关（null = 未配置 = 默认开启）
    const [w, as, ch, av] = await Promise.all(
      ['wallet_enabled', 'assistant_enabled', 'channel_enabled', 'account_verify_enabled'].map((k) => adminApi.configGet(k))
    )
    // 账户安全验证默认关闭（后端 SysConfigGet 默认 false），未配置视为关闭
    feature.value = {
      walletOn: w.data.data === null ? true : !!w.data.data,
      assistantOn: as.data.data === null ? true : !!as.data.data,
      channelOn: ch.data.data === null ? true : !!ch.data.data,
      accountVerifyOn: av.data.data === null ? false : !!av.data.data
    }
  const strKeys = ['app_name', 'brand_name', 'app_logo', 'brand_logo']
  const [an, bn, al, bl] = await Promise.all(strKeys.map((k) => adminApi.configGet(k)))
  brand.value = {
    appName: String(an.data.data || ''),
    brandName: String(bn.data.data || ''),
    appLogo: String(al.data.data || ''),
    brandLogo: String(bl.data.data || '')
  }
  const verKeys = ['app_version', 'android_version', 'ios_version', 'pc_version', 'update_log', 'android_url', 'ios_url', 'pc_url', 'hot_update_url']
  const [vVer, avVer, ivVer, pvVer, ulVer, adVer, ioVer, puVer, huVer] = await Promise.all(verKeys.map((k) => adminApi.configGet(k)))
  version.value = {
    appVersion: String(vVer.data.data || ''),
    androidVersion: String(avVer.data.data || ''),
    iosVersion: String(ivVer.data.data || ''),
    pcVersion: String(pvVer.data.data || ''),
    updateLog: String(ulVer.data.data || ''),
    androidUrl: String(adVer.data.data || ''),
    iosUrl: String(ioVer.data.data || ''),
    pcUrl: String(puVer.data.data || ''),
    hotUpdateUrl: String(huVer.data.data || '')
  }
  const smsKeys = ['sms_access_key', 'sms_secret', 'sms_sign_name', 'sms_template_code']
  const [sk, ss, sn, st] = await Promise.all(smsKeys.map((k) => adminApi.configGet(k)))
  sms.value = {
    accessKey: String(sk.data.data || ''),
    secret: String(ss.data.data || ''),
    signName: String(sn.data.data || ''),
    templateCode: String(st.data.data || '')
  }
  const trtcKeys = ['trtc_app_id', 'trtc_secret_key', 'call_engine', 'webrtc_ice_servers', 'livekit_url', 'livekit_api_key', 'livekit_api_secret']
  const [ta, tk, ce, wis, lku, lkk, lks] = await Promise.all(trtcKeys.map((k) => adminApi.configGet(k)))
  const engineVal = String(ce.data.data || 'trtc').toLowerCase()
  trtc.value = {
    appId: String(ta.data.data || ''),
    secretKey: String(tk.data.data || ''),
    engine: engineVal === 'webrtc' || engineVal === 'livekit' ? engineVal : 'trtc',
    // ICE 服务器 JSON：后端存的是 JSON 数组字符串，原样回填编辑框；空/脏值回退 []
    iceServers: (() => {
      const raw = String(wis.data.data || '').trim()
      try {
        const parsed = JSON.parse(raw || '[]')
        return Array.isArray(parsed) ? JSON.stringify(parsed, null, 2) : '[]'
      } catch (_) {
        return raw || '[]'
      }
    })(),
    livekitUrl: String(lku.data.data || ''),
    livekitApiKey: String(lkk.data.data || ''),
    livekitApiSecret: String(lks.data.data || '')
  }
  const aiKeys = ['ai_translate_enabled', 'ai_translate_api_base', 'ai_translate_api_key', 'ai_translate_model', 'ai_auto_daily_limit', 'ai_manual_daily_limit']
  const [aiEn, aiBase, aiKey, aiModel, aiAutoLim, aiManLim] = await Promise.all(aiKeys.map((k) => adminApi.configGet(k)))
  // 未配置过（null）默认开；显式 0/false/off 才关
  const aiEnStr = String(aiEn.data.data ?? '1').toLowerCase()
  aiTranslate.value = {
    enabled: !['0', 'false', 'off'].includes(aiEnStr),
    apiBase: String(aiBase.data.data || ''),
    apiKey: String(aiKey.data.data || ''),
    model: String(aiModel.data.data || ''),
    autoDailyLimit: Number(aiAutoLim.data.data ?? 50) || 0,
    manualDailyLimit: Number(aiManLim.data.data ?? 0) || 0
  }
  const smtpKeys = ['smtp_host', 'smtp_user', 'smtp_password', 'smtp_from']
  const [sh, su, sp, sf] = await Promise.all(smtpKeys.map((k) => adminApi.configGet(k)))
  smtp.value = {
    host: String(sh.data.data || ''),
    user: String(su.data.data || ''),
    password: String(sp.data.data || ''),
    from: String(sf.data.data || '')
  }
  const jpKeys = [
    'push_provider_ios',
    'jpush_enabled', 'jpush_app_key', 'jpush_master_secret', 'jpush_apns_production',
    'getui_enabled', 'getui_app_id', 'getui_app_key', 'getui_app_secret', 'getui_master_secret'
  ]
  const [pp, je, ja, jm, jp, ge, ga, gk, gs, gm] = await Promise.all(jpKeys.map((k) => adminApi.configGet(k)))
  jpush.value = {
    provider: String(pp.data.data || 'jpush') === 'getui' ? 'getui' : 'jpush',
    enabled: !!je.data.data,
    appKey: String(ja.data.data || ''),
    masterSecret: String(jm.data.data || ''),
    apnsProduction: !!jp.data.data,
    getuiEnabled: !!ge.data.data,
    getuiAppId: String(ga.data.data || ''),
    getuiAppKey: String(gk.data.data || ''),
    getuiAppSecret: String(gs.data.data || ''),
    getuiMasterSecret: String(gm.data.data || '')
  }
  const minioKeys = ['minio_endpoint', 'minio_public_url', 'minio_access_key', 'minio_secret_key', 'minio_bucket']
  const [me, mu, mk, ms, mb] = await Promise.all(minioKeys.map((k) => adminApi.configGet(k)))
  minio.value = {
    endpoint: String(me.data.data || ''),
    publicUrl: String(mu.data.data || ''),
    accessKey: String(mk.data.data || ''),
    secretKey: String(ms.data.data || ''),
    bucket: String(mb.data.data || '')
  }
  // 存储驱动 + 阿里云 OSS 配置
  const storageKeys = ['storage_driver', 'oss_endpoint', 'oss_bucket', 'oss_access_key_id', 'oss_access_key_secret', 'oss_public_url']
  const [sd, oe, ob, oi, osk, opu] = await Promise.all(storageKeys.map((k) => adminApi.configGet(k)))
  storage.value = { driver: String(sd.data.data || 'minio') === 'oss' ? 'oss' : 'minio' }
  oss.value = {
    endpoint: String(oe.data.data || ''),
    bucket: String(ob.data.data || ''),
    accessKeyId: String(oi.data.data || ''),
    accessKeySecret: String(osk.data.data || ''),
    publicUrl: String(opu.data.data || '')
  }
  const infraKeys = ['node_id', 'jwt_secret']
  const [ni, js] = await Promise.all(infraKeys.map((k) => adminApi.configGet(k)))
  infra.value = { nodeId: String(ni.data.data || ''), jwtSecret: String(js.data.data || '') }
  // 客服设置（GET 返回 SysConfigGet 解包后的 {autoAdd, mode, greeting}，未配置时为 null）
  try {
    const kf = await adminApi.configGet('kefu_config')
    const kfVal = (kf.data?.data ?? null) as Record<string, any> | null
    kefu.value = {
      autoAdd: !!kfVal?.autoAdd,
      mode: kfVal?.mode === 'all' ? 'all' : 'round',
      // 后端已兜底默认文案；前端再保险一次，避免首次进入空 textarea 没引导
      greeting: typeof kfVal?.greeting === 'string' ? kfVal.greeting : '你好，我是 {nickname}，很高兴为您服务~'
    }
  } catch { /* 未配置时保持默认 */ }
  // 群聊人数上限（GET 缺行返回 null → 0 = 不限制；兼容字符串存的数字）
  try {
    const gm = await adminApi.configGet('group_max_members')
    const gmVal = gm.data?.data
    groupMax.value = typeof gmVal === 'number' ? gmVal : parseInt(String(gmVal ?? 0), 10) || 0
  } catch { /* 未配置时保持 0（不限制） */ }
  const miscKeys = ['default_avatar', 'google_maps_api_key', 'map_engine', 'amap_js_key', 'amap_jscode', 'amap_web_key']
  const miscVals = await Promise.all(miscKeys.map((k) => adminApi.configGet(k)))
  const mv = Object.fromEntries(miscKeys.map((k, i) => [k, String(miscVals[i]?.data?.data || '')]))
  misc.value = {
    defaultAvatar: mv.default_avatar,
    reservedIds: '',
    googleMapsKey: mv.google_maps_api_key,
    mapEngine: mv.map_engine === 'amap' ? 'amap' : 'google',
    amapJsKey: mv.amap_js_key,
    amapJscode: mv.amap_jscode,
    amapWebKey: mv.amap_web_key
  }
  const ann = await adminApi.configGet('announcement')
  announcement.value = String(ann.data.data || '')
  // 注册默认进群配置 + 群列表选项
  void loadDefaultGroup()
  // 默认关注频道配置 + 频道列表选项
  void loadDefaultChannel()
  // 支付配置也一并拉下来（pay section 一进来就有值，不闪）
  void loadPayConfig()
})

async function loadDefaultGroup() {
  try {
    const dg = await adminApi.configGet('default_group_config')
    const val = (dg.data?.data ?? null) as Record<string, any> | null
    defaultGroup.value = {
      enabled: !!val?.enabled,
      // 兼容历史数字配置：统一转字符串；非法值过滤
      groupIds: Array.isArray(val?.groupIds)
        ? val.groupIds.map((n: any) => String(n).trim()).filter((s: string) => /^\d+$/.test(s))
        : []
    }
  } catch { /* 未配置时保持默认 */ }
  groupOptionsLoading.value = true
  try {
    const { data } = await adminApi.groups()
    // /admin/groups 返回 {list,total}（曾为裸数组）；兼容两种，避免对对象 .map 报错导致下拉永远为空
    const raw = (data?.data?.list ?? data?.data ?? []) as Array<Record<string, any>>
    const glist = Array.isArray(raw) ? raw : []
    // 群 ID 保持字符串（后端 json:"id,string"）：绝不能 Number()，雪花 ID 会丢精度
    groupOptions.value = glist.map((g) => ({
      label: `#${g.id} ${g.nameZh || g.nameEn || ''}`.trim(),
      value: String(g.id)
    }))
  } catch { /* 群列表读取失败不阻断 */ } finally {
    groupOptionsLoading.value = false
  }
}

// 默认关注频道：读取配置 + 拉频道选项（结构上与 loadDefaultGroup 同构）
async function loadDefaultChannel() {
  try {
    const dc = await adminApi.configGet('default_channel_config')
    const val = (dc.data?.data ?? null) as Record<string, any> | null
    defaultChannel.value = {
      enabled: !!val?.enabled,
      // 统一转字符串（兼容历史数字配置）；非法值过滤
      channelIds: Array.isArray(val?.channelIds)
        ? val.channelIds.map((n: any) => String(n).trim()).filter((s: string) => /^\d+$/.test(s))
        : [],
      welcomeMsg: typeof val?.welcomeMsg === 'string' ? val.welcomeMsg : ''
    }
  } catch { /* 未配置时保持默认 */ }
  channelOptionsLoading.value = true
  channelOptionsError.value = false
  try {
    // 频道 = type=3 的会话（复用会话表）。后端约定端点：GET /admin/conversations?type=3
    const { data } = await adminApi.adminConversations({ type: 3, page: 1, size: 200 })
    if (data.code !== 0) throw new Error(data.message || '频道列表读取失败')
    // 兼容 {list,total} 与裸数组两种返回；若后端返回全类型会话，这里按 type=3 再过滤一次
    const raw = (data.data?.list ?? data.data ?? []) as Array<Record<string, any>>
    const clist = (Array.isArray(raw) ? raw : []).filter((c) => !c.type || Number(c.type) === 3)
    // 频道 ID 保持字符串：绝不能 Number()，雪花 ID 会丢精度
    channelOptions.value = clist.map((c) => ({
      label: `#${c.id} ${c.nameZh || c.nameEn || c.name || ''}`.trim(),
      value: String(c.id)
    }))
  } catch {
    // 频道列表端点未就绪/读取失败：下拉为空并展示提示，不阻断配置读取与保存
    channelOptionsError.value = true
  } finally {
    channelOptionsLoading.value = false
  }
}

async function save(key: string, value: unknown) {
  const { data } = await adminApi.configSet(key, value)
  if (data.code === 0) Message.success('已保存')
  else Message.error(data.message)
}

async function saveBrand() {
  await Promise.all([
    adminApi.configSet('app_name', brand.value.appName),
    adminApi.configSet('brand_name', brand.value.brandName),
    adminApi.configSet('app_logo', brand.value.appLogo),
    adminApi.configSet('brand_logo', brand.value.brandLogo),
    adminApi.configSet('default_avatar', misc.value.defaultAvatar),
    adminApi.configSet('google_maps_api_key', misc.value.googleMapsKey),
    adminApi.configSet('map_engine', misc.value.mapEngine === 'amap' ? 'amap' : 'google'),
    adminApi.configSet('amap_js_key', misc.value.amapJsKey),
    adminApi.configSet('amap_jscode', misc.value.amapJscode),
    adminApi.configSet('amap_web_key', misc.value.amapWebKey)
  ])
  Message.success('品牌设置已保存')
}

async function saveVersion() {
  await Promise.all([
    adminApi.configSet('app_version', version.value.appVersion),
    adminApi.configSet('android_version', version.value.androidVersion),
    adminApi.configSet('ios_version', version.value.iosVersion),
    adminApi.configSet('pc_version', version.value.pcVersion),
    adminApi.configSet('update_log', version.value.updateLog),
    adminApi.configSet('android_url', version.value.androidUrl),
    adminApi.configSet('ios_url', version.value.iosUrl),
    adminApi.configSet('pc_url', version.value.pcUrl),
    adminApi.configSet('hot_update_url', version.value.hotUpdateUrl)
  ])
  Message.success('版本信息已保存')
}

async function saveSms() {
  await Promise.all([
    adminApi.configSet('sms_access_key', sms.value.accessKey),
    adminApi.configSet('sms_secret', sms.value.secret),
    adminApi.configSet('sms_sign_name', sms.value.signName),
    adminApi.configSet('sms_template_code', sms.value.templateCode)
  ])
  Message.success('短信配置已保存')
}

async function saveSmtp() {
  await Promise.all([
    adminApi.configSet('smtp_host', smtp.value.host),
    adminApi.configSet('smtp_user', smtp.value.user),
    adminApi.configSet('smtp_password', smtp.value.password),
    adminApi.configSet('smtp_from', smtp.value.from)
  ])
  Message.success('邮件配置已保存（即时生效）')
}

async function saveTrtc() {
  // ICE 服务器：必须是合法 JSON 数组才允许保存（脏 JSON 会让客户端 WebRTC 打洞配置失效）
  let iceRaw = '[]'
  if (trtc.value.engine === 'webrtc') {
    try {
      const parsed = JSON.parse(trtc.value.iceServers || '[]')
      if (!Array.isArray(parsed)) throw new Error('not array')
      iceRaw = JSON.stringify(parsed)
    } catch (_) {
      Message.error('ICE 服务器必须是 JSON 数组，示例：[{"urls":"turn:x.x.x.x:3478","username":"u","credential":"p"}]')
      return
    }
  }
  // LiveKit 模式：三项必填校验（url/key/secret 缺一项客户端进不了房间）
  if (trtc.value.engine === 'livekit') {
    if (!trtc.value.livekitUrl.trim() || !trtc.value.livekitApiKey.trim() || !trtc.value.livekitApiSecret.trim()) {
      Message.error('LiveKit 模式需要填写 SFU 地址、API Key、API Secret 三项')
      return
    }
  }
  await Promise.all([
    adminApi.configSet('trtc_app_id', trtc.value.appId),
    adminApi.configSet('trtc_secret_key', trtc.value.secretKey),
    adminApi.configSet('call_engine', trtc.value.engine),
    adminApi.configSet('webrtc_ice_servers', iceRaw),
    adminApi.configSet('livekit_url', trtc.value.livekitUrl.trim()),
    adminApi.configSet('livekit_api_key', trtc.value.livekitApiKey.trim()),
    adminApi.configSet('livekit_api_secret', trtc.value.livekitApiSecret.trim())
  ])
  const engineName = { trtc: 'TRTC 模式', webrtc: 'WebRTC 模式', livekit: 'LiveKit 模式' }[trtc.value.engine] || trtc.value.engine
  Message.success(`通话配置已保存（${engineName}，客户端下次发起通话生效）`)
}

async function saveAiTranslate() {
  savingAiTranslate.value = true
  try {
    await Promise.all([
      adminApi.configSet('ai_translate_enabled', aiTranslate.value.enabled),
      adminApi.configSet('ai_translate_api_base', aiTranslate.value.apiBase),
      adminApi.configSet('ai_translate_api_key', aiTranslate.value.apiKey),
      adminApi.configSet('ai_translate_model', aiTranslate.value.model),
      adminApi.configSet('ai_auto_daily_limit', aiTranslate.value.autoDailyLimit),
      adminApi.configSet('ai_manual_daily_limit', aiTranslate.value.manualDailyLimit)
    ])
    Message.success('AI 翻译配置已保存（关闭开关后客户端不再显示「译」按钮）')
  } finally {
    savingAiTranslate.value = false
  }
}

async function saveJpush() {
  await Promise.all([
    adminApi.configSet('push_provider_ios', jpush.value.provider),
    adminApi.configSet('jpush_enabled', jpush.value.enabled),
    adminApi.configSet('jpush_app_key', jpush.value.appKey),
    adminApi.configSet('jpush_master_secret', jpush.value.masterSecret),
    adminApi.configSet('jpush_apns_production', jpush.value.apnsProduction),
    adminApi.configSet('getui_enabled', jpush.value.getuiEnabled),
    adminApi.configSet('getui_app_id', jpush.value.getuiAppId),
    adminApi.configSet('getui_app_key', jpush.value.getuiAppKey),
    adminApi.configSet('getui_app_secret', jpush.value.getuiAppSecret),
    adminApi.configSet('getui_master_secret', jpush.value.getuiMasterSecret)
  ])
  Message.success('推送配置已保存（服务端 1 分钟内生效）')
}

async function saveStorage() {
  // 驱动 + 当前驱动的配置一并写回（两套配置各自独立保存，互不覆盖）
  const tasks: Promise<any>[] = [
    adminApi.configSet('storage_driver', storage.value.driver),
    adminApi.configSet('minio_endpoint', minio.value.endpoint),
    adminApi.configSet('minio_public_url', minio.value.publicUrl),
    adminApi.configSet('minio_access_key', minio.value.accessKey),
    adminApi.configSet('minio_secret_key', minio.value.secretKey),
    adminApi.configSet('minio_bucket', minio.value.bucket),
    adminApi.configSet('oss_endpoint', oss.value.endpoint),
    adminApi.configSet('oss_bucket', oss.value.bucket),
    adminApi.configSet('oss_access_key_id', oss.value.accessKeyId),
    adminApi.configSet('oss_access_key_secret', oss.value.accessKeySecret),
    adminApi.configSet('oss_public_url', oss.value.publicUrl)
  ]
  await Promise.all(tasks)
  Message.success(storage.value.driver === 'oss' ? '已切换为阿里云 OSS，配置已保存（即时生效）' : '已切换为 MinIO，配置已保存（即时生效）')
}

async function saveInfra() {
  await Promise.all([
    adminApi.configSet('node_id', infra.value.nodeId),
    adminApi.configSet('jwt_secret', infra.value.jwtSecret)
  ])
  Message.success('基础设施配置已保存')
}

async function saveAnnouncement() {
  await adminApi.configSet('announcement', announcement.value)
  Message.success('公告已保存')
}

async function saveGroupMax() {
  savingGroupMax.value = true
  try {
    // 独立标量键 group_max_members；0 = 不限制
    const { data } = await adminApi.configSet('group_max_members', groupMax.value)
    if (data.code === 0) Message.success('群聊设置已保存')
    else Message.error(data.message)
  } finally {
    savingGroupMax.value = false
  }
}

// 聊天设置 Tab 化后的统一保存：四个子块一次写回，任一失败提示，全部成功才报成功
async function saveChatAll() {
  savingChat.value = true
  try {
    const results = await Promise.allSettled([
      adminApi.configSet('kefu_config', {
        autoAdd: kefu.value.autoAdd,
        mode: kefu.value.mode,
        greeting: kefu.value.greeting
      }),
      adminApi.configSet('default_group_config', {
        enabled: defaultGroup.value.enabled,
        groupIds: defaultGroup.value.groupIds
      }),
      adminApi.configSet('default_channel_config', {
        enabled: defaultChannel.value.enabled,
        channelIds: defaultChannel.value.channelIds,
        welcomeMsg: defaultChannel.value.welcomeMsg
      }),
      adminApi.configSet('group_max_members', groupMax.value)
    ])
    // 兼容历史保存逻辑的判返回结构：code===0 视为成功
    const failed = results.filter((r) => {
      if (r.status === 'rejected') return true
      return r.value?.data?.code !== 0
    })
    if (failed.length) Message.error('部分设置保存失败，请检查后重试')
    else Message.success('聊天设置已保存')
  } catch (e: any) {
    Message.error('保存失败' + (e?.message ? '：' + e.message : ''))
  } finally {
    savingChat.value = false
  }
}
</script>

<style scoped>
.config-page { display: flex; gap: var(--app-space-lg); height: 100%; min-height: 600px; }

/* 左侧分区导航 */
.config-nav {
  width: 180px;
  display: flex; flex-direction: column; gap: 4px;
  padding: 12px;
  background: var(--app-bg-card);
  border: 1px solid var(--app-border-2);
  border-radius: var(--app-radius-lg);
  box-shadow: var(--app-shadow-card);
  flex-shrink: 0;
  height: fit-content;
  position: sticky; top: 0;
}
.nav-btn {
  display: flex; align-items: center; gap: 10px;
  width: 100%; padding: 10px 12px;
  background: transparent; border: none;
  border-radius: var(--app-radius-md);
  color: var(--app-text-2);
  font-size: var(--app-font-size-base);
  cursor: pointer; text-align: left;
  transition: background var(--app-transition-base), color var(--app-transition-base);
}
.nav-btn:hover { background: var(--app-border-2); color: var(--app-text-1); }
.nav-btn.active {
  background: var(--app-primary-bg);
  color: var(--app-primary);
  font-weight: var(--app-font-weight-medium);
}
.nav-btn :deep(svg) { width: 18px; height: 18px; flex-shrink: 0; }

/* 右侧内容 */
.config-body { flex: 1; min-width: 0; }
.section { max-width: 780px; }
.section-title { margin: 0 0 6px; font-size: var(--app-font-size-xl); font-weight: var(--app-font-weight-semibold); color: var(--app-text-1); }
.section-desc { margin: 0 0 16px; font-size: var(--app-font-size-sm); color: var(--app-text-3); }
.form-card { border-radius: var(--app-radius-lg); }
/* 未选中的推送服务商卡片降透明度提示（内容仍可编辑，方便提前填参数） */
.form-card-dim { opacity: 0.55; }
/* 聊天设置 section 内部三块子标题 */
.sub-title {
  margin: 24px 0 4px;
  font-size: var(--app-font-size-base);
  font-weight: var(--app-font-weight-semibold);
  color: var(--app-text-1);
}
.sub-title + .section-desc { margin-bottom: 10px; }
.chat-section > :first-child { margin-top: 0; }
/* 聊天设置 Tab 统一保存条 */
.chat-save-bar { display: flex; justify-content: flex-end; padding: 4px 0 0; border-top: 1px solid var(--app-border-2); margin-top: 4px; padding-top: 14px; }
.form-card :deep(.arco-form-item-label) { padding-bottom: 6px; font-weight: 500; }
.form-card :deep(.arco-form-item) { margin-bottom: 18px; }
.brand-card :deep(.arco-form-item:last-child) { margin-bottom: 0; }
.form-actions {
  display: flex; align-items: center; justify-content: flex-start;
  padding-top: 8px;
}

/* ================= 支付配置 section 样式（**单卡片**包住两个 Tab 全部内容） ================= */
.pay-section { max-width: 1180px; }
.pay-tabs :deep(.arco-tabs-header) { margin: 0 0 20px; }
.tab-title { display: inline-flex; align-items: center; gap: 6px; font-weight: 600; }

/* 整张大卡片：圆角 + 投影 + 内边距更大，让用户一眼看到"一张卡片" */
.pay-card {
  border-radius: 18px;
  background: #fff;
  box-shadow:
    0 1px 2px rgba(0,0,0,0.04),
    0 6px 18px rgba(15,20,40,0.05),
    0 18px 50px rgba(15,20,40,0.04);
  overflow: hidden;
}
.pay-card :deep(.arco-card-body) { padding: 24px 28px 0; }

/* 行：标题 + 开关 */
.pay-row { display: flex; align-items: flex-start; gap: 20px; }
.pay-row.head-row {
  padding: 14px 18px;
  border-radius: 14px;
  background: linear-gradient(135deg, #f4f7ff 0%, #ffffff 100%);
  border: 1px solid #e2e9ff;
  margin-bottom: 20px;
}
.pay-row .row-title { font-size: 15px; font-weight: 600; color: var(--app-text-1); margin-bottom: 4px; }
.pay-row .row-desc  { font-size: 12.5px; color: var(--app-text-3); }
.pay-row > :last-child { margin-left: auto; }

/* 分隔标题（卡片内部分段用，替代之前丑 a-divider） */
.pay-divider {
  display: flex; align-items: center;
  margin: 22px 0 14px;
  color: var(--app-text-2);
  font-weight: 600;
  font-size: 14px;
  position: relative;
}
.pay-divider::before {
  content: "";
  width: 3px; height: 14px;
  background: linear-gradient(180deg, #165dff, #6aa0ff);
  border-radius: 3px;
  margin-right: 10px;
  box-shadow: 0 1px 3px rgba(22,93,255,.35);
}
.pay-divider .d-sub { font-weight: 400; font-size: 12px; color: var(--app-text-3); margin-left: 6px; }

/* 收款码 3 列：卡片**内部** 3 个通道盒（不用 a-card 套，就盒子+品牌色左边条） */
.channels-grid {
  display: grid; grid-template-columns: repeat(3, 1fr);
  gap: 18px;
}
@media (max-width: 1100px) { .channels-grid { grid-template-columns: repeat(2, 1fr); } }
@media (max-width: 720px)  { .channels-grid { grid-template-columns: 1fr; } }

.ch {
  border-radius: 16px;
  background: #fafbfc;
  border: 1px solid var(--app-border-2);
  padding: 16px;
  display: flex; flex-direction: column;
  transition: all .2s ease;
  position: relative;
  overflow: hidden;
}
.ch::before {
  content: ""; position: absolute; left: 0; top: 0; bottom: 0; width: 4px;
}
.ch.wechat::before   { background: linear-gradient(180deg, #07c160, #63e09b); }
.ch.alipay::before   { background: linear-gradient(180deg, #1677ff, #76aaff); }
.ch.bank::before     { background: linear-gradient(180deg, #ff7d00, #ffc285); }
.ch:hover {
  background: #fff;
  transform: translateY(-2px);
  box-shadow: 0 8px 22px rgba(0,0,0,0.06);
}
.ch-head { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; padding-left: 4px; }
.ch-brand {
  display: inline-flex; align-items: center; gap: 6px;
  font-weight: 700; color: #fff; font-size: 13.5px;
  padding: 3px 10px; border-radius: 8px;
}
.ch.wechat .ch-brand { background: linear-gradient(135deg, #07c160,#2ed573); }
.ch.alipay .ch-brand { background: linear-gradient(135deg, #1677ff,#3b8cff); }
.ch.bank   .ch-brand { background: linear-gradient(135deg, #ff7d00,#ff9d3b); }

.ch-main { flex: 1; display: flex; flex-direction: column; align-items: center; gap: 12px; }
.ch-ops  { display: flex; gap: 8px; }

/* 提现提示卡 */
.pay-alert { border-radius: 12px; margin-bottom: 0; }
.chip {
  display: inline-block; padding: 1px 8px; border-radius: 999px;
  font-size: 12px; font-weight: 500; margin: 0 4px;
}
.chip.ok   { background: #e8f9ef; color: #07c160; border: 1px solid #bef0cf; }
.chip.warn { background: #fff3e6; color: #ff7d00; border: 1px solid #ffd7af; }

/* 提现 2 栏：门槛 左 / 费率+预览 右 */
.withdraw-two-col {
  display: grid; grid-template-columns: 1fr 1fr; gap: 18px;
  margin-top: 4px;
}
@media (max-width: 900px) { .withdraw-two-col { grid-template-columns: 1fr; } }
.col-block {
  border-radius: 14px;
  background: #fafbfc;
  border: 1px solid var(--app-border-2);
  padding: 16px 18px;
}
.col-title {
  font-weight: 600;
  padding: 4px 0 12px;
  margin-bottom: 10px;
  border-bottom: 1px dashed var(--app-border-2);
  display: flex; align-items: center;
}
.muted { color: var(--app-text-3); font-size: 12px; }

/* 提现费率滑条 */
.slider-row { display: flex; align-items: center; gap: 16px; }
.slider-row :deep(.arco-slider) {
  flex: 1 1 auto;
  width: auto;
  min-width: 200px;
  pointer-events: auto;
  overflow: visible;
}
.slider-row :deep(.arco-slider-track) { pointer-events: auto; }
.slider-row :deep(.arco-slider-btn) { pointer-events: auto; z-index: 10; }
.rate-chip {
  flex: 0 0 auto;
  min-width: 72px; text-align: center;
  padding: 6px 10px; border-radius: 10px;
  background: linear-gradient(135deg, #fff6ee, #ffe6d1);
  color: #ff7d00; font-weight: 700; border: 1px solid #ffd0a6;
  font-size: 14px;
}

/* 预览卡 */
.preview-box {
  margin-top: 8px; padding: 14px 16px; border-radius: 14px;
  background: linear-gradient(135deg, #f4f7ff 0%, #ffffff 60%, #fff3ed 100%);
  border: 1px solid #dfe6ff;
}
.preview-title { font-size: 12.5px; color: var(--app-text-3); margin-bottom: 10px; letter-spacing: .5px; }
.preview-row { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.preview-row .label { font-size: 12px; color: var(--app-text-3); margin-bottom: 6px; }
.preview-row .value { font-size: 20px; font-weight: 700; color: var(--app-text-1); }
.preview-row .value.fee { color: #f53f3f; }
.preview-row .value.ok  { color: #07c160; }

/* 卡片**内部**的底部保存条（sticky 视口底部，滚一屏就能看见） */
.pay-actions-inner {
  margin: 20px -28px 0;
  padding: 14px 28px;
  border-top: 1px solid var(--app-border-2);
  background: #fcfdff;
  display: flex; justify-content: flex-end;
  position: sticky; bottom: 0; z-index: 2;
  backdrop-filter: blur(6px);
}
.hint { color: var(--app-text-3); font-size: 12px; margin-top: 6px; }
</style>
