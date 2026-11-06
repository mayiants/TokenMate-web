# 前端模板国际化审计报告

> **⚠️ HISTORICAL** — This audit was the baseline; the i18n migration has since been completed
>
> **审计范围**: `web/templates/**/*.html`  
> **审计时间": 2026-06-01  
> **统计**: 53 个文件包含中文硬编码，共 2475 处

---


## addresses.html (78 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <button class="btn btn-primary btn-sm" onclick="showAddModal()">添加地址</button>` |
| 7 | `  <p class="text-sm text-muted mt-2">管理您的收货地址，可用于发票邮寄等。</p>` |
| 14 | `      <span class="modal-title text-error">确认删除</span>` |
| 18 | `      <p class="text-sm">确定要删除此地址吗？</p>` |
| 19 | `      <p class="text-xs text-muted mt-2">删除后无法恢复。</p>` |
| 22 | `      <button class="btn btn-secondary" onclick="document.getElementById('delete-confirm-modal').style.display='none'">取消</button>` |
| 23 | `      <button class="btn btn-danger" id="delete-confirm-btn" onclick="executeDelete()">删除</button>` |
| 32 | `      <span class="modal-title" id="msg-title">提示</span>` |
| 39 | `      <button class="btn btn-primary" onclick="document.getElementById('msg-modal').style.display='none'">确定</button>` |
| 46 | `  <div class="text-center py-8 text-muted">加载中...</div>` |
| 53 | `      <span class="modal-title" id="modal-title">添加地址</span>` |
| 59 | `        <label class="form-label">国家/地区 *</label>` |
| 61 | `          <option value="CN">中国</option>` |
| 62 | `          <option value="HK">中国香港</option>` |
| 63 | `          <option value="TW">中国台湾</option>` |
| 64 | `          <option value="JP">日本</option>` |
| 65 | `          <option value="KR">韩国</option>` |
| 66 | `          <option value="SG">新加坡</option>` |
| 67 | `          <option value="MY">马来西亚</option>` |
| 68 | `          <option value="TH">泰国</option>` |
| 69 | `          <option value="VN">越南</option>` |
| 70 | `          <option value="PH">菲律宾</option>` |
| 71 | `          <option value="ID">印度尼西亚</option>` |
| 72 | `          <option value="IN">印度</option>` |
| 73 | `          <option value="AU">澳大利亚</option>` |
| 74 | `          <option value="NZ">新西兰</option>` |
| 75 | `          <option value="US">美国</option>` |
| 76 | `          <option value="CA">加拿大</option>` |
| 77 | `          <option value="GB">英国</option>` |
| 78 | `          <option value="DE">德国</option>` |
| 79 | `          <option value="FR">法国</option>` |
| 80 | `          <option value="IT">意大利</option>` |
| 81 | `          <option value="ES">西班牙</option>` |
| 82 | `          <option value="NL">荷兰</option>` |
| 83 | `          <option value="SE">瑞典</option>` |
| 84 | `          <option value="CH">瑞士</option>` |
| 85 | `          <option value="RU">俄罗斯</option>` |
| 86 | `          <option value="BR">巴西</option>` |
| 87 | `          <option value="MX">墨西哥</option>` |
| 88 | `          <option value="AE">阿联酋</option>` |
| 89 | `          <option value="SA">沙特阿拉伯</option>` |
| 90 | `          <option value="OTHER">其他</option>` |
| 95 | `          <label class="form-label">收件人 *</label>` |
| 96 | `          <input type="text" id="addr-name" class="form-input" placeholder="姓名">` |
| 99 | `          <label class="form-label">联系电话</label>` |
| 108 | `            <label class="form-label">省/直辖市 *</label>` |
| 109 | `            <input type="text" id="addr-province" class="form-input" placeholder="北京">` |
| 112 | `            <label class="form-label">市 *</label>` |
| 113 | `            <input type="text" id="addr-city" class="form-input" placeholder="北京市">` |
| 116 | `            <label class="form-label">区/县</label>` |
| 117 | `            <input type="text" id="addr-district" class="form-input" placeholder="朝阳区">` |
| 126 | `            <label class="form-label">州/省</label>` |
| 130 | `            <label class="form-label">城市</label>` |
| 137 | `        <label class="form-label">详细地址 *</label>` |
| 138 | `        <input type="text" id="addr-detail" class="form-input" placeholder="街道、门牌号、楼层等">` |
| 142 | `          <label class="form-label">邮编</label>` |
| 148 | `            <span class="form-label mb-0">设为默认地址</span>` |
| 155 | `      <button class="btn btn-secondary" onclick="document.getElementById('address-modal').style.display='none'">取消</button>` |
| 156 | `      <button class="btn btn-primary" onclick="saveAddress()">保存</button>` |
| 166 | `  'CN':'中国','HK':'中国香港','TW':'中国台湾','JP':'日本','KR':'韩国',` |
| 167 | `  'SG':'新加坡','MY':'马来西亚','TH':'泰国','VN':'越南','PH':'菲律宾',` |
| 168 | `  'ID':'印度尼西亚','IN':'印度','AU':'澳大利亚','NZ':'新西兰',` |
| 169 | `  'US':'美国','CA':'加拿大','GB':'英国','DE':'德国','FR':'法国',` |
| 170 | `  'IT':'意大利','ES':'西班牙','NL':'荷兰','SE':'瑞典','CH':'瑞士',` |
| 171 | `  'RU':'俄罗斯','BR':'巴西','MX':'墨西哥','AE':'阿联酋','SA':'沙特阿拉伯','OTHER':'其他'` |
| 182 | `    document.getElementById('address-list').innerHTML = '<div class="text-center py-8 text-muted">加载失败</div>';` |
| 189 | `    container.innerHTML = '<div class="card"><div class="text-center py-8 text-muted">暂无地址，点击上方按钮添加</div></div>';` |
| 208 | `    if (addr.is_default) html += '        <span class="badge badge-primary">默认</span>';` |
| 212 | `    if (addr.postal_code) html += '      <div class="text-xs text-muted mt-1">邮编: ' + addr.postal_code + '</div>';` |
| 215 | `    html += '      <button class="btn btn-sm btn-ghost" onclick="editAddress(\'' + addr.id + '\')">编辑</button>';` |
| 216 | `    if (!addr.is_default) html += '      <button class="btn btn-sm btn-ghost" onclick="setDefault(\'' + addr.id + '\')">设为默认</button>';` |
| 217 | `    html += '      <button class="btn btn-sm btn-ghost text-error" onclick="deleteAddress(\'' + addr.id + '\')">删除</button>';` |
| 227 | `  document.getElementById('modal-title').textContent = '添加地址';` |
| 249 | `  document.getElementById('modal-title').textContent = '编辑地址';` |
| 292 | `    document.getElementById('addr-result').innerHTML = '<div class="flash flash-error text-sm">请填写收件人和详细地址</div>';` |
| 308 | `    showMsg('保存成功', id ? '地址已更新' : '地址已添加');` |
| 311 | `    document.getElementById('addr-result').innerHTML = '<div class="flash flash-error text-sm">' + (r.error?.message \|\| '保存失败') + '</div>';` |
| 336 | `  showMsg('删除成功', '地址已删除');` |

## admin-announcements.html (38 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <button class="btn btn-primary" onclick="openCreateModal()">新建公告</button>` |
| 12 | `          <th>标题</th>` |
| 13 | `          <th>类型</th>` |
| 14 | `          <th>状态</th>` |
| 15 | `          <th>置顶</th>` |
| 16 | `          <th>已读</th>` |
| 17 | `          <th>创建时间</th>` |
| 18 | `          <th>操作</th>` |
| 30 | `      <h2 id="modal-title">新建公告</h2>` |
| 36 | `        <label class="form-label">标题</label>` |
| 37 | `        <input type="text" id="ann-title" class="form-input" placeholder="公告标题">` |
| 40 | `        <label class="form-label">内容</label>` |
| 41 | `        <textarea id="ann-content" class="form-input" rows="6" placeholder="公告内容" style="resize:vertical"></textarea>` |
| 45 | `          <label class="form-label">类型</label>` |
| 47 | `            <option value="info">信息</option>` |
| 48 | `            <option value="warning">警告</option>` |
| 49 | `            <option value="maintenance">维护</option>` |
| 50 | `            <option value="update">更新</option>` |
| 54 | `          <label class="form-label">优先级</label>` |
| 60 | `          <label class="form-label">展示开始</label>` |
| 64 | `          <label class="form-label">展示结束</label>` |
| 70 | `          <input type="checkbox" id="ann-pinned"> 置顶` |
| 73 | `          <input type="checkbox" id="ann-popup"> 弹窗展示` |
| 78 | `      <button class="btn btn-secondary" onclick="closeModal()">取消</button>` |
| 79 | `      <button class="btn btn-primary" onclick="saveAnnouncement()">保存</button>` |
| 110 | `      var typeLabel = {info:'信息',warning:'警告',maintenance:'维护',update:'更新'}[a.type]\|\|a.type;` |
| 111 | `      var statusBadge = a.status==='published'?'<span class="badge badge-success">已发布</span>':` |
| 112 | `                        a.status==='archived'?'<span class="badge badge-info">已归档</span>':` |
| 113 | `                        '<span class="badge badge-warning">草稿</span>';` |
| 116 | `        actions += '<button class="btn btn-sm btn-primary" onclick="publishAnnouncement('+a.id+')">发布</button> ';` |
| 119 | `        actions += '<button class="btn btn-sm btn-secondary" onclick="archiveAnnouncement('+a.id+')">归档</button> ';` |
| 121 | `      actions += '<button class="btn btn-sm btn-secondary" onclick="editAnnouncement('+a.id+')">编辑</button> ';` |
| 122 | `      actions += '<button class="btn btn-sm btn-danger" onclick="deleteAnnouncement('+a.id+')">删除</button>';` |
| 123 | `      list.innerHTML += '<tr><td>'+a.id+'</td><td>'+a.title+'</td><td>'+typeLabel+'</td><td>'+statusBadge+'</td><td>'+((a.pinned)?'是':'否')+'</td><td>'` |
| 129 | `  document.getElementById('modal-title').textContent = '新建公告';` |
| 144 | `    document.getElementById('modal-title').textContent = '编辑公告';` |
| 184 | `      showMessage('错误', (data.error && data.error.message) ? data.error.message : '保存失败');` |
| 201 | `  showConfirm('确认删除', '确定删除此公告？', function() {` |

## admin-audit-logs.html (29 处)

| 行号 | 代码 |
|------|------|
| 7 | `      <input type="text" id="log-search" class="form-input" placeholder="搜索用户或操作..." style="width: 200px" onkeyup="filterLogs()">` |
| 9 | `        <option value="">所有分类</option>` |
| 10 | `        <option value="auth">认证</option>` |
| 12 | `        <option value="billing">账单</option>` |
| 13 | `        <option value="enterprise">企业</option>` |
| 14 | `        <option value="gdpr">GDPR 权利行使</option>` |
| 15 | `        <option value="profile">个人资料</option>` |
| 16 | `        <option value="security">安全</option>` |
| 17 | `        <option value="user">用户管理</option>` |
| 18 | `        <option value="admin">系统管理</option>` |
| 21 | `        <option value="">所有级别</option>` |
| 26 | `      <a class="btn btn-secondary" href="/admin/audit-logs/export" download="audit-logs.csv">导出日志</a>` |
| 33 | `      <div class="stat-label">总日志</div>` |
| 37 | `      <div class="stat-label">错误数</div>` |
| 41 | `      <div class="stat-label">警告数</div>` |
| 51 | `          <th>时间</th>` |
| 52 | `          <th>用户</th>` |
| 53 | `          <th>分类</th>` |
| 54 | `          <th>操作</th>` |
| 55 | `          <th>级别</th>` |
| 56 | `          <th>消息</th>` |
| 85 | `            <button class="btn btn-sm btn-ghost" onclick="viewLogDetail('{{.ID}}')" title="详情">` |
| 95 | `  <p class="text-muted text-center mt-4">暂无审计日志</p>` |
| 99 | `    <span class="pagination-info">共 {{$d.Total}} 条，第 {{$d.Page}} / {{$d.TotalPages}} 页</span>` |
| 101 | `    <a class="btn btn-sm btn-secondary" href="/admin/audit-logs?page={{int (sub $d.Page 1)}}">上一页</a>` |
| 104 | `    <a class="btn btn-sm btn-secondary" href="/admin/audit-logs?page={{int (add $d.Page 1)}}">下一页</a>` |
| 114 | `      <span class="modal-title">日志详情</span>` |
| 150 | `      tr.innerHTML = '<td colspan="7" class="text-center text-muted py-4">没有匹配的日志</td>';` |
| 166 | `      document.getElementById('log-detail-body').innerHTML = '<div class="text-center py-4 text-muted">加载失败</div>';` |

## admin-enterprise.html (141 处)

| 行号 | 代码 |
|------|------|
| 7 | `      <button class="btn btn-primary" onclick="showCreateModal()">创建企业</button>` |
| 14 | `      <div class="stat-label">总企业数</div>` |
| 18 | `      <div class="stat-label">活跃企业</div>` |
| 22 | `      <div class="stat-label">已禁用</div>` |
| 36 | `          <th>企业名称</th>` |
| 37 | `          <th>联系邮箱</th>` |
| 38 | `          <th>成员数</th>` |
| 39 | `          <th>配额</th>` |
| 40 | `          <th>已用</th>` |
| 41 | `          <th>信用额度</th>` |
| 42 | `          <th>已用信用</th>` |
| 43 | `          <th>倍率</th>` |
| 44 | `          <th>状态</th>` |
| 45 | `          <th style="text-align:right">操作</th>` |
| 60 | `            {{if eq .Status "active"}}<span class="badge badge-soft-success">活跃</span>` |
| 61 | `            {{else if eq .Status "suspended"}}<span class="badge badge-soft-error">禁用</span>` |
| 63 | `            {{else if eq .Status "deleted"}}<span class="badge badge-outline">已删除</span>` |
| 68 | `              <button class="btn btn-sm btn-secondary" onclick="editEnterprise('{{.ID}}')">编辑</button>` |
| 69 | `              <button class="btn btn-sm btn-primary" onclick="showTopupModal('{{.ID}}', '{{.Name}}')">充值</button>` |
| 70 | `              <button class="btn btn-sm btn-secondary" onclick="manageMembers('{{.ID}}')">成员</button>` |
| 72 | `              <button class="btn btn-sm btn-warning" onclick="confirmSuspend('{{.ID}}', '{{.Name}}')">禁用</button>` |
| 74 | `              <button class="btn btn-sm btn-success" onclick="confirmActivate('{{.ID}}', '{{.Name}}')">启用</button>` |
| 76 | `              <button class="btn btn-sm btn-danger" onclick="confirmDelete('{{.ID}}', '{{.Name}}')">删除</button>` |
| 86 | `    <span class="pagination-info">共 {{$d.Total}} 条，第 {{$d.Page}} / {{$d.TotalPages}} 页</span>` |
| 88 | `    <a class="btn btn-sm btn-secondary" href="/admin/enterprise?page={{int (sub $d.Page 1)}}">上一页</a>` |
| 91 | `    <a class="btn btn-sm btn-secondary" href="/admin/enterprise?page={{int (add $d.Page 1)}}">下一页</a>` |
| 96 | `  <p class="text-muted text-center mt-4">暂无企业</p>` |
| 104 | `      <span class="modal-title">创建企业</span>` |
| 111 | `          <label class="form-label">企业名称 *</label>` |
| 112 | `          <input type="text" name="name" class="form-input" required placeholder="公司全称">` |
| 115 | `          <label class="form-label">联系邮箱 *</label>` |
| 120 | `            <label class="form-label">Token 配额</label>` |
| 122 | `            <p class="text-xs text-muted mt-1">0 = 不限制</p>` |
| 125 | `            <label class="form-label">信用额度 (微美元)</label>` |
| 127 | `            <p class="text-xs text-muted mt-1">1000000 = $1.00，0 = 未配置</p>` |
| 132 | `            <label class="form-label">最大成员数</label>` |
| 136 | `            <label class="form-label">最大管理员数</label>` |
| 141 | `          <label class="form-label">计费周期</label>` |
| 143 | `            <option value="monthly">月付</option>` |
| 144 | `            <option value="quarterly">季付</option>` |
| 145 | `            <option value="yearly">年付</option>` |
| 149 | `          <label class="form-label">允许的邮箱域名（可选）</label>` |
| 151 | `          <p class="text-xs text-muted mt-1">用逗号分隔，只允许这些域名的邮箱加入。留空不限制。</p>` |
| 155 | `        <button type="button" class="btn btn-secondary" onclick="closeModal('create-modal')">取消</button>` |
| 156 | `        <button type="submit" class="btn btn-primary">创建</button>` |
| 166 | `      <span class="modal-title">编辑企业</span>` |
| 174 | `          <label class="form-label">企业名称 *</label>` |
| 175 | `          <input type="text" name="name" id="edit-name" class="form-input" required placeholder="公司全称">` |
| 178 | `          <label class="form-label">联系邮箱 *</label>` |
| 183 | `            <label class="form-label">Token 配额</label>` |
| 185 | `            <p class="text-xs text-muted mt-1">0 = 不限制</p>` |
| 188 | `            <label class="form-label">信用额度 (微美元)</label>` |
| 195 | `            <label class="form-label">定价倍率</label>` |
| 197 | `            <p class="text-xs text-muted mt-1">0.8 = 八折，1.0 = 原价</p>` |
| 200 | `            <label class="form-label">最大成员数</label>` |
| 204 | `            <label class="form-label">最大管理员数</label>` |
| 209 | `          <label class="form-label">计费周期</label>` |
| 211 | `            <option value="monthly">月付</option>` |
| 212 | `            <option value="quarterly">季付</option>` |
| 213 | `            <option value="yearly">年付</option>` |
| 217 | `          <label class="form-label">允许的邮箱域名（可选）</label>` |
| 219 | `          <p class="text-xs text-muted mt-1">用逗号分隔，只允许这些域名的邮箱加入。留空不限制。</p>` |
| 223 | `        <button type="button" class="btn btn-secondary" onclick="closeModal('edit-modal')">取消</button>` |
| 224 | `        <button type="submit" class="btn btn-primary">保存</button>` |
| 234 | `      <span class="modal-title">成员管理</span>` |
| 241 | `          <option value="member">普通成员</option>` |
| 242 | `          <option value="admin">企业管理员</option>` |
| 244 | `        <button class="btn btn-primary" onclick="inviteMember()">邀请</button>` |
| 246 | `      <p class="text-xs text-muted mb-4">邀请的企业管理员可以管理本企业成员和 API Key。</p>` |
| 248 | `        <p class="text-muted text-center">加载中...</p>` |
| 258 | `      <span class="modal-title">企业充值</span>` |
| 266 | `        <label class="form-label">追加 Token 配额</label>` |
| 267 | `        <input type="number" id="topup-quota" class="form-input" value="0" min="0" placeholder="0 = 不追加">` |
| 268 | `        <p class="text-xs text-muted mt-1">在现有配额基础上增加</p>` |
| 271 | `        <label class="form-label">追加信用额度 (微美元)</label>` |
| 272 | `        <input type="number" id="topup-credit" class="form-input" value="0" min="0" placeholder="0 = 不追加">` |
| 273 | `        <p class="text-xs text-muted mt-1">1000000 = $1.00，在现有信用额度基础上增加</p>` |
| 276 | `        <label class="form-label">备注</label>` |
| 277 | `        <input type="text" id="topup-reason" class="form-input" placeholder="充值原因（可选）">` |
| 281 | `      <button class="btn btn-secondary" onclick="closeModal('topup-modal')">取消</button>` |
| 282 | `      <button class="btn btn-primary" onclick="submitTopup()">确认充值</button>` |
| 291 | `      <span class="modal-title">企业详情</span>` |
| 295 | `      <div class="text-center py-4 text-muted">加载中...</div>` |
| 298 | `      <button class="btn btn-secondary" onclick="closeModal('detail-modal')">关闭</button>` |
| 351 | `      showMessage('创建成功', '企业已创建');` |
| 354 | `      showMessage('创建失败', result.error ? result.error.message : '未知错误');` |
| 359 | `    showMessage('请求失败', e.message);` |
| 365 | `  document.getElementById('detail-body').innerHTML = '<div class="text-center py-4 text-muted">加载中...</div>';` |
| 373 | `        html += '<div class="flex justify-between"><span class="text-muted">名称</span><strong>' + e.name + '</strong></div>';` |
| 374 | `        html += '<div class="flex justify-between"><span class="text-muted">联系邮箱</span><span>' + (e.contact_email \|\| '-') + '</span></div>';` |
| 375 | `        html += '<div class="flex justify-between"><span class="text-muted">状态</span><span class="badge badge-' + (e.status === 'active' ? 'success' :` |
| 376 | `        html += '<div class="flex justify-between"><span class="text-muted">成员数</span><span>' + e.member_count + '</span></div>';` |
| 377 | `        html += '<div class="flex justify-between"><span class="text-muted">配额</span><span>' + (e.quota_total \|\| 0).toLocaleString() + ' tokens</spa` |
| 378 | `        html += '<div class="flex justify-between"><span class="text-muted">已用配额</span><span>' + (e.quota_used \|\| 0).toLocaleString() + ' tokens</sp` |
| 379 | `        html += '<div class="flex justify-between"><span class="text-muted">信用额度</span><span>$' + ((e.credit_total \|\| 0) / 1000000).toFixed(2) + '</` |
| 380 | `        html += '<div class="flex justify-between"><span class="text-muted">已用信用</span><span>$' + ((e.credit_used \|\| 0) / 1000000).toFixed(2) + '</s` |
| 381 | `        html += '<div class="flex justify-between"><span class="text-muted">定价倍率</span><span>' + (e.pricing_ratio \|\| 1.0).toFixed(2) + 'x</span></di` |
| 382 | `        html += '<div class="flex justify-between"><span class="text-muted">创建时间</span><span>' + formatDateTime(e.created_at) + '</span></div>';` |
| 388 | `      document.getElementById('detail-body').innerHTML = '<div class="text-center text-danger py-4">加载失败: ' + e.message + '</div>';` |
| 412 | `      showMessage('加载失败', e.message);` |
| 439 | `      showMessage('保存成功', '企业信息已更新');` |
| 442 | `      showMessage('保存失败', result.error ? result.error.message : '未知错误');` |
| 447 | `    showMessage('请求失败', e.message);` |
| 458 | `  document.getElementById('members-list').innerHTML = '<div class="text-center text-muted py-4">加载中...</div>';` |
| 466 | `        html += '<div style="margin-bottom:16px;"><div class="font-medium" style="margin-bottom:8px; color:var(--warning);">待处理邀请 (' + result.invitati` |
| 467 | `        html += '<table class="data-table"><thead><tr><th>邮箱</th><th>角色</th><th>状态</th><th>操作</th></tr></thead><tbody>';` |
| 469 | `          var roleLabel = inv.role === 'admin' ? '管理员' : '成员';` |
| 470 | `          var statusLabel = inv.status === 'email_confirmed' ? '已确认邮箱' : '待确认';` |
| 472 | `          html += '<td><button class="btn btn-sm btn-secondary" onclick="approveInvitation(\'' + inv.id + '\')">通过</button> ';` |
| 473 | `          html += '<button class="btn btn-sm btn-danger" onclick="rejectInvitation(\'' + inv.id + '\')">拒绝</button></td></tr>';` |
| 480 | `        html += '<div><div class="font-medium" style="margin-bottom:8px;">已加入成员 (' + result.members.length + ')</div>';` |
| 481 | `        html += '<table class="data-table"><thead><tr><th>邮箱</th><th>姓名</th><th>角色</th><th>操作</th></tr></thead><tbody>';` |
| 483 | `          var roleLabel = m.role === 'admin' \|\| m.role === 'enterprise_admin' ? '管理员' : '成员';` |
| 485 | `          html += '<td><button class="btn btn-sm btn-danger" onclick="removeMember(\'' + m.id + '\')">移除</button></td></tr>';` |
| 489 | `        html = '<p class="text-muted text-center">暂无成员</p>';` |
| 495 | `      document.getElementById('members-list').innerHTML = '<div class="text-center text-danger py-4">加载失败: ' + e.message + '</div>';` |
| 503 | `    showMessage('错误', '请输入邮箱');` |
| 514 | `      showMessage('邀请已发送', '邀请邮件已发送至 ' + email);` |
| 518 | `      showMessage('邀请失败', result.error ? result.error.message : '未知错误');` |
| 522 | `    showMessage('邀请失败', e.message);` |
| 527 | `  showConfirm('确认移除', '确定要移除此成员吗？', function() {` |
| 534 | `          showMessage('移除失败', result.error ? result.error.message : '未知错误');` |
| 538 | `        showMessage('移除失败', e.message);` |
| 550 | `        showMessage('审批失败', result.error ? result.error.message : '未知错误');` |
| 554 | `      showMessage('审批失败', e.message);` |
| 559 | `  showConfirm('确认拒绝', '确定要拒绝该邀请吗？', function() {` |
| 566 | `          showMessage('拒绝失败', result.error ? result.error.message : '未知错误');` |
| 570 | `        showMessage('拒绝失败', e.message);` |
| 576 | `  showConfirm('确认禁用', '确定要禁用企业 "' + name + '" 吗？禁用后该企业所有成员将无法使用。', function() {` |
| 583 | `          showMessage('操作失败', result.error ? result.error.message : '未知错误');` |
| 587 | `        showMessage('操作失败', e.message);` |
| 593 | `  showConfirm('确认启用', '确定要启用企业 "' + name + '" 吗？', function() {` |
| 600 | `          showMessage('操作失败', result.error ? result.error.message : '未知错误');` |
| 604 | `        showMessage('操作失败', e.message);` |
| 610 | `  showConfirm('确认删除', '⚠️ 确定要永久删除企业 "' + name + '" 吗？\n\n此操作将：\n- 移除所有成员的企业关联\n- 删除企业配额和设置\n- 此操作不可撤销', function() {` |
| 617 | `          showMessage('删除失败', result.error ? result.error.message : '未知错误');` |
| 621 | `        showMessage('删除失败', e.message);` |
| 644 | `    showMessage('错误', '请至少填写一项充值金额');` |
| 661 | `      showMessage('充值成功', '企业配额/信用已更新');` |
| 664 | `      showMessage('充值失败', result.error ? result.error.message : '未知错误');` |
| 669 | `    showMessage('请求失败', e.message);` |

## admin-group-edit.html (24 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <h1 class="card-title">{{if $group.ID}}编辑{{else}}新建{{end}}用户组</h1>` |
| 14 | `        <label class="form-label">名称 *</label>` |
| 18 | `        <label class="form-label">状态</label>` |
| 20 | `          <option value="active" {{if eq $group.Status "active"}}selected{{end}}>活跃</option>` |
| 21 | `          <option value="inactive" {{if eq $group.Status "inactive"}}selected{{end}}>禁用</option>` |
| 25 | `        <label class="form-label">类型</label>` |
| 27 | `          <option value="general" {{if eq $group.GroupType "general"}}selected{{end}}>标准</option>` |
| 28 | `          <option value="economy" {{if eq $group.GroupType "economy"}}selected{{end}}>折扣</option>` |
| 29 | `          <option value="subscription" {{if eq $group.GroupType "subscription"}}selected{{end}}>套餐</option>` |
| 30 | `          <option value="enterprise" {{if eq $group.GroupType "enterprise"}}selected{{end}}>企业</option>` |
| 34 | `        <label class="form-label">计费</label>` |
| 37 | `          <option value="request" {{if eq $group.BillingMode "request"}}selected{{end}}>请求</option>` |
| 41 | `        <label class="form-label">倍率</label>` |
| 45 | `        <label class="form-label">路由通道</label>` |
| 47 | `          <option value="standard" {{if eq $group.RoutingTrack "standard"}}selected{{end}}>标准通道</option>` |
| 54 | `      <label class="form-label">描述</label>` |
| 58 | `    <h3 class="mt-4 mb-2">限流配置 <small class="text-muted">（留空 = 不限制）</small></h3>` |
| 77 | `        <label class="rate-label">RP/月</label>` |
| 97 | `        <label class="rate-label">TP/月</label>` |
| 102 | `    <h3 class="mt-4 mb-2">模型列表 <small class="text-muted">（不勾选 = 可使用所有模型）</small></h3>` |
| 104 | `      <input type="text" id="model-search" class="form-input" placeholder="搜索模型..." oninput="filterModels()" style="max-width: 300px;">` |
| 111 | `      <button type="button" class="btn btn-secondary" onclick="history.back()">取消</button>` |
| 112 | `      <button type="button" class="btn btn-primary" onclick="saveGroup()">保存</button>` |
| 222 | `      showMessage('保存失败', data.error?.message \|\| '保存失败');` |

## admin-groups.html (86 处)

| 行号 | 代码 |
|------|------|
| 8 | `        <option value="">全部类型</option>` |
| 9 | `        <option value="general">标准</option>` |
| 10 | `        <option value="economy">折扣</option>` |
| 11 | `        <option value="subscription">套餐</option>` |
| 12 | `        <option value="enterprise">企业</option>` |
| 15 | `        <option value="">全部状态</option>` |
| 16 | `        <option value="active">活跃</option>` |
| 17 | `        <option value="inactive">禁用</option>` |
| 19 | `      <button class="btn btn-primary" onclick="showCreateModal()">+ 新建</button>` |
| 26 | `      <div class="stat-label">系统用户组</div>` |
| 30 | `      <div class="stat-label">企业自建组</div>` |
| 36 | `  <!-- 系统用户组 -->` |
| 37 | `  <h2 class="card-title mt-4" style="font-size:14px; color:var(--text-muted); border-bottom:1px solid var(--border); padding-bottom:8px;">系统用户组</h2>` |
| 43 | `          <th>名称</th>` |
| 44 | `          <th>类型</th>` |
| 45 | `          <th>计费</th>` |
| 46 | `          <th>倍率</th>` |
| 47 | `          <th>路由通道</th>` |
| 48 | `          <th>模型数</th>` |
| 49 | `          <th>成员</th>` |
| 50 | `          <th>Key数</th>` |
| 51 | `          <th>状态</th>` |
| 52 | `          <th style="text-align:right">操作</th>` |
| 60 | `            {{if .IsDefault}}<span class="badge badge-info" style="font-size:10px;margin-left:4px;">默认</span>{{end}}` |
| 64 | `              {{if eq .GroupType "economy"}}折扣{{else}}{{if eq .GroupType "subscription"}}套餐{{else}}{{if eq .GroupType "enterprise"}}企业{{else}}标准{{end}` |
| 67 | `          <td>{{if eq .BillingMode "request"}}按请求{{else}}按Token{{end}}</td>` |
| 69 | `          <td>{{if eq .RoutingTrack "bedrock"}}<span class="badge badge-info">AWS Bedrock</span>{{else}}<span class="badge badge-secondary">标准通道</span` |
| 70 | `          <td>{{if .ModelCount}}{{.ModelCount}}{{else}}全部模型{{end}}</td>` |
| 75 | `              {{if eq .Status "active"}}活跃{{else}}禁用{{end}}` |
| 80 | `              <a href="/admin/groups/{{.ID}}/edit" class="btn btn-sm btn-secondary">编辑</a>` |
| 81 | `              <button class="btn btn-sm btn-{{if eq .Status "active"}}warning{{else}}success{{end}}" onclick="toggleGroup('{{.ID}}', '{{.Status}}')">{` |
| 83 | `              <button class="btn btn-sm btn-danger" onclick="confirmDeleteGroup('{{.ID}}', '{{.Name}}', {{.MemberCount}}, {{.KeyCount}})">删除</button>` |
| 93 | `  <p class="text-muted text-center mt-2">暂无系统用户组</p>` |
| 96 | `  <!-- 企业自建组 -->` |
| 97 | `  <h2 class="card-title mt-4" style="font-size:14px; color:var(--text-muted); border-bottom:1px solid var(--border); padding-bottom:8px;">企业自建组</h2>` |
| 103 | `          <th>名称</th>` |
| 104 | `          <th>所属企业</th>` |
| 105 | `          <th>类型</th>` |
| 106 | `          <th>计费</th>` |
| 107 | `          <th>倍率</th>` |
| 108 | `          <th>路由通道</th>` |
| 109 | `          <th>模型数</th>` |
| 110 | `          <th>成员</th>` |
| 111 | `          <th>Key数</th>` |
| 112 | `          <th>状态</th>` |
| 113 | `          <th style="text-align:right">操作</th>` |
| 122 | `            <span class="group-type-label type-enterprise">企业</span>` |
| 124 | `          <td>{{if eq .BillingMode "request"}}按请求{{else}}按Token{{end}}</td>` |
| 126 | `          <td>{{if eq .RoutingTrack "bedrock"}}<span class="badge badge-info">AWS Bedrock</span>{{else}}<span class="badge badge-secondary">标准通道</span` |
| 127 | `          <td>{{if .ModelCount}}{{.ModelCount}}{{else}}全部模型{{end}}</td>` |
| 132 | `              {{if eq .Status "active"}}活跃{{else}}禁用{{end}}` |
| 137 | `              <a href="/admin/groups/{{.ID}}/edit" class="btn btn-sm btn-secondary">编辑</a>` |
| 138 | `              <button class="btn btn-sm btn-{{if eq .Status "active"}}warning{{else}}success{{end}}" onclick="toggleGroup('{{.ID}}', '{{.Status}}')">{` |
| 139 | `              <button class="btn btn-sm btn-danger" onclick="confirmDeleteGroup('{{.ID}}', '{{.Name}}', {{.MemberCount}}, {{.KeyCount}})">删除</button>` |
| 148 | `  <p class="text-muted text-center mt-2">暂无企业自建组</p>` |
| 164 | `      <span class="modal-title">新建用户组</span>` |
| 171 | `          <label class="form-label">名称 *</label>` |
| 172 | `          <input type="text" name="name" class="form-input" required placeholder="例如: general-1">` |
| 175 | `          <label class="form-label">描述</label>` |
| 176 | `          <input type="text" name="description" class="form-input" placeholder="可选">` |
| 180 | `            <label class="form-label">类型</label>` |
| 182 | `              <option value="general">标准</option>` |
| 183 | `              <option value="economy">折扣</option>` |
| 184 | `              <option value="subscription">套餐</option>` |
| 185 | `              <option value="enterprise">企业</option>` |
| 189 | `            <label class="form-label">计费模式</label>` |
| 191 | `              <option value="token">按Token</option>` |
| 192 | `              <option value="request">按请求</option>` |
| 196 | `            <label class="form-label">计费倍率</label>` |
| 201 | `          <label class="form-label">路由通道</label>` |
| 203 | `            <option value="standard">标准通道 (reseller)</option>` |
| 204 | `            <option value="bedrock">AWS Bedrock (官方)</option>` |
| 206 | `          <small class="form-hint">标准通道走现有 reseller，Bedrock 走 AWS 官方通道（需要配置 Bedrock upstream key）</small>` |
| 210 | `        <button type="button" class="btn btn-secondary" onclick="closeModal('create-modal')">取消</button>` |
| 211 | `        <button type="submit" class="btn btn-primary">创建</button>` |
| 263 | `      showMessage('创建失败', result.error ? result.error.message : '未知错误');` |
| 267 | `    showMessage('请求失败', err.message);` |
| 273 | `  var action = newStatus === 'active' ? '启用' : '禁用';` |
| 274 | `  showConfirm('确认' + action, '确定要' + action + '此用户组吗？', function() {` |
| 285 | `        showMessage('操作失败', result.error ? result.error.message : '未知错误');` |
| 289 | `      showMessage('请求失败', err.message);` |
| 296 | `    showMessage('无法删除', '该用户组还有 ' + memberCount + ' 个成员，请先移除成员后再删除。');` |
| 300 | `    showMessage('无法删除', '该用户组还有 ' + keyCount + ' 个API Key关联，请先删除或迁移这些Key。');` |
| 303 | `  showConfirm('删除用户组', '确定要删除用户组 "' + name + '" 吗？此操作不可恢复。', function() {` |
| 310 | `          showMessage('删除失败', result.error ? result.error.message : '未知错误');` |
| 314 | `        showMessage('请求失败', err.message);` |

## admin-invitations.html (74 处)

| 行号 | 代码 |
|------|------|
| 6 | `    <button class="btn btn-primary" onclick="openSettingsModal()">编辑配置</button>` |
| 9 | `    <strong>规则说明：</strong>每个码使用次数上限 100 次，有效期上限 365 天，每人邀请码数上限 100 个。以上限制为系统硬上限，配置值超过时自动取上限值。邀请奖励自动转入赠送余额（gift_balance），消费优先级最高。` |
| 14 | `        <tr><th>配置项</th><th>当前值</th><th>说明</th></tr>` |
| 24 | `    <h2 class="card-title">欢迎码管理</h2>` |
| 25 | `    <button class="btn btn-primary" onclick="openWelcomeModal()">编辑配置</button>` |
| 28 | `    欢迎码在用户注册时自动发放，属于一次性注册优惠。与邀请码类似，都是注册时触发的奖励机制。` |
| 33 | `        <tr><th>兑换码</th><th>金额</th><th>状态</th><th>过期时间</th><th>创建时间</th></tr>` |
| 36 | `        <tr><td colspan="5" class="text-center text-muted py-4">加载中...</td></tr>` |
| 46 | `      <h3>编辑欢迎码</h3>` |
| 51 | `        <label class="form-label">金额（$）</label>` |
| 53 | `        <div style="font-size:.8rem;color:var(--text-muted);margin-top:4px">新用户注册时自动获得的赠送金额</div>` |
| 56 | `        <label class="form-label">状态</label>` |
| 58 | `          <option value="active">活跃</option>` |
| 59 | `          <option value="disabled">禁用</option>` |
| 61 | `        <div style="font-size:.8rem;color:var(--text-muted);margin-top:4px">禁用后新用户注册不再自动获得奖励</div>` |
| 64 | `        <label class="form-label">过期时间（可选）</label>` |
| 69 | `      <button class="btn btn-secondary" onclick="closeWelcomeModal()">取消</button>` |
| 70 | `      <button class="btn btn-primary" onclick="saveWelcomeCode()">保存</button>` |
| 79 | `      <h3>编辑邀请码配置</h3>` |
| 85 | `          <label class="form-label">每人最大邀请码数</label>` |
| 87 | `          <div style="font-size:.8rem;color:var(--text-muted);margin-top:4px">每个用户最多可生成的邀请码数量</div>` |
| 90 | `          <label class="form-label">每个码最大使用次数</label>` |
| 92 | `          <div style="font-size:.8rem;color:var(--text-muted);margin-top:4px">每个邀请码最多可被使用的次数（上限100）</div>` |
| 95 | `          <label class="form-label">有效期（天）</label>` |
| 97 | `          <div style="font-size:.8rem;color:var(--text-muted);margin-top:4px">邀请码从生成到失效的天数（上限365天）</div>` |
| 100 | `          <label class="form-label">邀请人奖励（$）</label>` |
| 102 | `          <div style="font-size:.8rem;color:var(--text-muted);margin-top:4px">成功邀请一人后邀请人获得的金额</div>` |
| 105 | `          <label class="form-label">被邀请人奖励（$）</label>` |
| 107 | `          <div style="font-size:.8rem;color:var(--text-muted);margin-top:4px">使用邀请码注册后获得的金额</div>` |
| 110 | `          <label class="form-label">奖励自动转入余额</label>` |
| 112 | `            <option value="true">自动转入（推荐）</option>` |
| 113 | `            <option value="false">手动转入</option>` |
| 115 | `          <div style="font-size:.8rem;color:var(--text-muted);margin-top:4px">自动转入：奖励直接进入可用余额；手动转入：奖励存入邀请额度，需用户手动操作</div>` |
| 120 | `      <button class="btn btn-secondary" onclick="closeSettingsModal()">取消</button>` |
| 121 | `      <button class="btn btn-primary" onclick="saveAllSettings()">保存</button>` |
| 129 | `    <h2 class="card-title">邀请码列表</h2>` |
| 132 | `        <option value="all">全部</option>` |
| 133 | `        <option value="active">活跃</option>` |
| 134 | `        <option value="disabled">禁用</option>` |
| 135 | `        <option value="expired">已过期</option>` |
| 142 | `        <tr><th>ID</th><th>邀请码</th><th>邀请人</th><th>已用/上限</th><th>邀请奖励</th><th>注册奖励</th><th>有效期</th><th>状态</th><th>创建时间</th><th>操作</th></tr>` |
| 149 | `    <button class="btn btn-sm btn-secondary" id="codes-prev-btn" onclick="codesPage--;loadCodes()" disabled>上一页</button>` |
| 150 | `    <button class="btn btn-sm btn-secondary" id="codes-next-btn" onclick="codesPage++;loadCodes()">下一页</button>` |
| 156 | `    <h2 class="card-title">邀请记录</h2>` |
| 161 | `        <tr><th>邀请码</th><th>邀请人</th><th>被邀请人</th><th>邀请人奖励</th><th>注册奖励</th><th>时间</th></tr>` |
| 168 | `    <button class="btn btn-sm btn-secondary" id="records-prev-btn" onclick="recordsPage--;loadRecords()" disabled>上一页</button>` |
| 169 | `    <button class="btn btn-sm btn-secondary" id="records-next-btn" onclick="recordsPage++;loadRecords()">下一页</button>` |
| 224 | `  {key: 'invitation_max_codes_per_user', label: '每人最大邀请码数', desc: '每个用户最多可生成的邀请码数量', inputId: 'edit-max-codes'},` |
| 225 | `  {key: 'invitation_default_max_uses', label: '每个码最大使用次数', desc: '每个邀请码最多可被使用的次数', inputId: 'edit-max-uses'},` |
| 226 | `  {key: 'invitation_default_expire_days', label: '有效期（天）', desc: '邀请码从生成到失效的天数', inputId: 'edit-expire-days'},` |
| 227 | `  {key: 'invitation_inviter_reward', label: '邀请人奖励（$）', desc: '成功邀请一人后邀请人获得的金额', inputId: 'edit-inviter-reward'},` |
| 228 | `  {key: 'invitation_invitee_reward', label: '被邀请人奖励（$）', desc: '使用邀请码注册后获得的金额', inputId: 'edit-invitee-reward'},` |
| 229 | `  {key: 'invitation_auto_transfer_enabled', label: '奖励自动转入余额', desc: '自动转入：奖励直接进入可用余额；手动转入：奖励存入邀请额度', inputId: 'edit-auto-transfer'}` |
| 255 | `      displayValue = displayValue === 'true' ? '自动转入' : displayValue === 'false' ? '手动转入' : '-';` |
| 306 | `    showMessage('成功', '邀请码配置已保存');` |
| 322 | `      list.innerHTML = '<tr><td colspan="10" class="text-center text-muted py-4">暂无数据</td></tr>';` |
| 326 | `      var statusBadge = c.status==='active'?'<span class="badge badge-success">活跃</span>':'<span class="badge badge-warning">禁用</span>';` |
| 327 | `      var expiresAt = c.expires_at ? formatDate(c.expires_at) : '永久';` |
| 329 | `        '<button class="btn btn-sm btn-secondary" onclick="disableCode('+c.id+')">禁用</button>':` |
| 330 | `        '<button class="btn btn-sm btn-primary" onclick="enableCode('+c.id+')">启用</button>';` |
| 331 | `      list.innerHTML += '<tr><td>'+c.id+'</td><td><span>'+c.code+'</span></td><td>'+c.inviter_name+' ('+c.inviter_email+')</td><td>'+c.use_count+'/'+(` |
| 338 | `      document.getElementById('codes-page-info').textContent = '共 ' + data.total + ' 条，第 ' + codesPage + ' / ' + codesTotalPages + ' 页';` |
| 357 | `      list.innerHTML = '<tr><td colspan="6" class="text-center text-muted py-4">暂无数据</td></tr>';` |
| 361 | `      var inviterReward = r.reward_granted?'<span class="badge badge-success">已发放</span>':'<span class="badge badge-warning">未发放</span>';` |
| 362 | `      var inviteeReward = r.invitee_reward_granted?'<span class="badge badge-success">已发放</span>':'<span class="badge badge-warning">未发放</span>';` |
| 370 | `      document.getElementById('records-page-info').textContent = '共 ' + data.total + ' 条，第 ' + recordsPage + ' / ' + recordsTotalPages + ' 页';` |
| 380 | `  showConfirm('确认禁用', '确定要禁用该邀请码吗？禁用后该邀请码将无法使用。', function() {` |
| 386 | `  showConfirm('确认启用', '确定要启用该邀请码吗？', function() {` |
| 397 | `      list.innerHTML = '<tr><td colspan="5" class="text-center text-muted py-4">暂无欢迎码，请点击"编辑欢迎码"创建</td></tr>';` |
| 400 | `    var statusBadge = wc.status === 'active' ? '<span class="badge badge-success">活跃</span>' : '<span class="badge badge-error">禁用</span>';` |
| 401 | `    var expiresAt = wc.expires_at ? formatDate(wc.expires_at) : '永久';` |
| 436 | `    showMessage('错误', '请输入有效金额');` |
| 447 | `      showMessage('成功', data.message \|\| '欢迎码已保存');` |
| 449 | `      showMessage('错误', data.error?.message \|\| '保存失败');` |

## admin-keypool.html (123 处)

| 行号 | 代码 |
|------|------|
| 8 | `        <option value="">所有供应商</option>` |
| 12 | `        <option value="other">其他</option>` |
| 14 | `      <button class="btn btn-secondary" onclick="validateAllKeys()">批量验证</button>` |
| 15 | `      <button class="btn btn-secondary" onclick="showImportModal()">批量导入</button>` |
| 16 | `      <button class="btn btn-secondary" onclick="exportKeys()">批量导出</button>` |
| 17 | `      <button class="btn btn-primary" onclick="showAddKeyModal()">添加Key</button>` |
| 24 | `      <div class="stat-label">总Key数</div>` |
| 28 | `      <div class="stat-label">活跃Key</div>` |
| 32 | `      <div class="stat-label">禁用Key</div>` |
| 36 | `      <div class="stat-label">验证通过</div>` |
| 46 | `          <th>供应商</th>` |
| 47 | `          <th>预览</th>` |
| 48 | `          <th>RPM限制</th>` |
| 49 | `          <th>TPM限制</th>` |
| 50 | `          <th>状态</th>` |
| 51 | `          <th>验证状态</th>` |
| 52 | `          <th>创建时间</th>` |
| 81 | `              <button class="btn btn-sm btn-ghost" onclick="validateKey('{{.ID}}')">验证</button>` |
| 82 | `              <button class="btn btn-sm btn-secondary" onclick="editKey('{{.ID}}', '{{.Provider}}', {{.RpmLimit}}, {{.TpmLimit}}, {{.QuotaLimit}}, '{{` |
| 83 | `              <button class="btn btn-sm btn-secondary" onclick="toggleKey('{{.ID}}', {{if .Enabled}}false{{else}}true{{end}})">{{if .Enabled}}禁用{{else` |
| 84 | `              <button class="btn btn-sm btn-danger" onclick="deleteKey('{{.ID}}')">删除</button>` |
| 94 | `    <span class="pagination-info">共 {{$d.Total}} 条，第 {{$d.Page}} / {{$d.TotalPages}} 页</span>` |
| 96 | `    <a class="btn btn-sm btn-secondary" href="/admin/keypool?page={{int (sub $d.Page 1)}}">上一页</a>` |
| 99 | `    <a class="btn btn-sm btn-secondary" href="/admin/keypool?page={{int (add $d.Page 1)}}">下一页</a>` |
| 104 | `  <p class="text-muted text-center mt-4">暂无上游Key</p>` |
| 112 | `      <span class="modal-title">添加上游Key</span>` |
| 119 | `          <label class="form-label">供应商</label>` |
| 121 | `            <option value="">选择供应商</option>` |
| 125 | `            <option value="other">其他</option>` |
| 131 | `          <p class="text-xs text-muted mt-1" id="key-api-key-hint">Key将被加密存储</p>` |
| 134 | `          <label class="form-label">RPM限制 (可选)</label>` |
| 136 | `          <p class="text-xs text-muted mt-1">每分钟请求数限制</p>` |
| 139 | `          <label class="form-label">TPM限制 (可选)</label>` |
| 141 | `          <p class="text-xs text-muted mt-1">每分钟Token数限制</p>` |
| 144 | `          <label class="form-label">Quota总量 (可选)</label>` |
| 145 | `          <input type="number" name="quota_limit" class="form-input" placeholder="留空=无限制" min="0">` |
| 146 | `          <p class="text-xs text-muted mt-1">该Key的总Token配额上限</p>` |
| 149 | `          <label class="form-label">Tier级别</label>` |
| 151 | `            <option value="default">default（标准）</option>` |
| 152 | `            <option value="premium">premium（优质）</option>` |
| 153 | `            <option value="enterprise">enterprise（企业）</option>` |
| 154 | `            <option value="economy">economy（经济）</option>` |
| 158 | `          <label class="form-label">Base URL (可选)</label>` |
| 160 | `          <p class="text-xs text-muted mt-1">留空使用默认URL</p>` |
| 164 | `          <button type="submit" class="btn btn-primary">添加</button>` |
| 175 | `      <span class="modal-title">编辑上行Key</span>` |
| 183 | `          <label class="form-label">供应商</label>` |
| 187 | `          <label class="form-label">Tier级别</label>` |
| 189 | `            <option value="default">default（标准）</option>` |
| 190 | `            <option value="premium">premium（优质）</option>` |
| 191 | `            <option value="enterprise">enterprise（企业）</option>` |
| 192 | `            <option value="economy">economy（经济）</option>` |
| 196 | `          <label class="form-label">RPM限制</label>` |
| 200 | `          <label class="form-label">TPM限制</label>` |
| 204 | `          <label class="form-label">Quota总量 (0=无限制)</label>` |
| 212 | `          <button type="button" class="btn btn-secondary" onclick="document.getElementById('edit-key-modal').style.display='none'">取消</button>` |
| 213 | `          <button type="submit" class="btn btn-primary">保存</button>` |
| 224 | `      <span class="modal-title">批量导入Key</span>` |
| 230 | `          <label class="form-label">导入方式</label>` |
| 232 | `            <option value="single">单供应商批量导入 (高效)</option>` |
| 233 | `            <option value="mixed">混合供应商导入 (迁移)</option>` |
| 240 | `            <label class="form-label">供应商</label>` |
| 242 | `              <option value="">选择供应商</option>` |
| 246 | `              <option value="other">其他</option>` |
| 251 | `              <label class="form-label">RPM 限制 (可选)</label>` |
| 255 | `              <label class="form-label">TPM 限制 (可选)</label>` |
| 260 | `            <label class="form-label">Base URL (可选)</label>` |
| 261 | `            <input type="text" name="single_base_url" id="import-base-url" class="form-input" placeholder="留空使用默认URL">` |
| 264 | `            <label class="form-label">API Keys (每行一个，最多100行)</label>` |
| 268 | `            <p class="text-xs text-muted mt-1">每行一个Key，超过100行请使用文件上传</p>` |
| 275 | `            <label class="form-label">Key数据 (最多100行)</label>` |
| 276 | `            <p class="text-xs text-muted mb-2">格式: 供应商,Key,Base URL,RPM限制,TPM限制,启用状态</p>` |
| 279 | `            <p class="text-xs text-muted mt-1">超过100行请使用文件上传</p>` |
| 284 | `          <label class="form-label">或上传文件 (无限制)</label>` |
| 289 | `          <button type="button" class="btn btn-secondary" onclick="downloadTemplate()">下载模板</button>` |
| 290 | `          <button type="button" class="btn btn-secondary" onclick="document.getElementById('import-modal').style.display='none'">取消</button>` |
| 291 | `          <button type="submit" class="btn btn-primary">导入</button>` |
| 343 | `    apiKeyHint.textContent = 'AWS 格式: access_key_id:secret_access_key (将被加密存储)';` |
| 346 | `    apiKeyHint.textContent = 'Key将被加密存储';` |
| 353 | `    baseUrlInput.placeholder = providerBaseUrls[provider] + ' (默认)';` |
| 364 | `  showMessage('成功', message);` |
| 368 | `  showMessage('错误', message);` |
| 409 | `        showMessage('添加失败', data.error.message \|\| '添加失败');` |
| 411 | `        showMessage('添加成功', 'Key已添加', function() { window.location.reload(); });` |
| 416 | `      showMessage('添加失败', e.message);` |
| 451 | `        showMessage('编辑失败', data.error.message \|\| '编辑失败');` |
| 458 | `      showMessage('编辑失败', e.message);` |
| 463 | `  showConfirm('删除Key', '确定要删除此Key吗？', function() {` |
| 470 | `          showError('删除失败: ' + JSON.stringify(data.error));` |
| 473 | `      .catch(e => showError('删除失败: ' + e.message));` |
| 478 | `  var action = enable ? '启用' : '禁用';` |
| 479 | `  showConfirm(action + 'Key', '确定要' + action + '此Key吗？', function() {` |
| 490 | `          showError(action + '失败: ' + JSON.stringify(data.error));` |
| 493 | `      .catch(e => showError(action + '失败: ' + e.message));` |
| 498 | `  showMessage('验证中', '正在验证Key...');` |
| 503 | `        showSuccess('Key验证成功! 状态: ' + data.status + (data.models ? ', 模型数: ' + data.models.length : ''));` |
| 505 | `        showError('Key验证结果: ' + data.status);` |
| 509 | `    .catch(e => showError('验证失败: ' + e.message));` |
| 513 | `  showConfirm('批量验证', '确定要验证所有Key吗？', function() {` |
| 514 | `    showMessage('验证中', '正在批量验证Key...');` |
| 522 | `        showSuccess('验证完成! 有效: ' + data.validated + ', 无效: ' + data.invalid);` |
| 525 | `      .catch(e => showError('验证失败: ' + e.message));` |
| 547 | `    showError('超过 ' + maxLines + ' 行限制，请使用文件上传');` |
| 564 | `    baseUrlInput.placeholder = providerBaseUrls[provider] + ' (默认)';` |
| 576 | `    csv = '# 单供应商导入模板\n';` |
| 577 | `    csv += '# 每行一个Key，无需其他信息\n';` |
| 582 | `    csv = '# 混合供应商导入模板\n';` |
| 583 | `    csv += '# 格式: 供应商,Key,Base URL,RPM限制,TPM限制,启用状态\n';` |
| 618 | `      showError('请选择供应商');` |
| 622 | `      showError('请输入要导入的Key');` |
| 633 | `      showError('没有有效的Key');` |
| 653 | `          showError('导入失败: ' + (data.error.message \|\| JSON.stringify(data.error)));` |
| 655 | `          showMessage('成功', '导入成功! 已添加 ' + (data.imported \|\| 0) + ' 个Key, 跳过 ' + (data.skipped \|\| 0) + ' 个', function() {` |
| 660 | `      .catch(e => showError('导入失败: ' + e.message));` |
| 667 | `      showError('请输入要导入的Key数据');` |
| 678 | `        showError('第 ' + (i + 1) + ' 行格式错误，需要: 供应商,Key');` |
| 694 | `      showError('没有有效的Key数据');` |
| 708 | `          showError('导入失败: ' + (data.error.message \|\| JSON.stringify(data.error)));` |
| 710 | `          showMessage('成功', '导入成功! 已添加 ' + (data.imported \|\| 0) + ' 个Key, 跳过 ' + (data.skipped \|\| 0) + ' 个', function() {` |
| 715 | `      .catch(e => showError('导入失败: ' + e.message));` |
| 724 | `        showError('导出失败: ' + (data.error.message \|\| JSON.stringify(data.error)));` |
| 743 | `        showError('没有可导出的Key');` |
| 746 | `    .catch(e => showError('导出失败: ' + e.message));` |

## admin-models-aws.html (54 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <h1 class="card-title">模型管理-AWS</h1>` |
| 7 | `      <button class="btn btn-primary" onclick="syncBedrock()">同步 AWS Bedrock</button>` |
| 19 | `        <p class="text-sm" style="color: var(--text);">AWS Bedrock 模型独立管理。支持从 AWS API 同步模型、预览定价、批量导入，以及管理已同步的 AWS 模型状态。</p>` |
| 20 | `        <p class="text-xs mt-1" style="color: var(--text-muted);">AWS 模型 provider 标识为 <code>amazon</code>，与通用模型分开管理。同步前会显示预览窗口，可编辑每个模型的定价。</p>` |
| 28 | `      <div class="stat-label">已同步模型</div>` |
| 32 | `      <div class="stat-label">正常服务</div>` |
| 36 | `      <div class="stat-label">已暂停</div>` |
| 40 | `      <div class="stat-label">待确认</div>` |
| 50 | `          <th>API模型名称</th>` |
| 52 | `          <th style="text-align:right">输入 ($/1M)</th>` |
| 53 | `          <th style="text-align:right">输出 ($/1M)</th>` |
| 54 | `          <th style="text-align:right">缓存命中 ($/1M)</th>` |
| 55 | `          <th>状态</th>` |
| 56 | `          <th style="text-align:right">操作</th>` |
| 60 | `        <tr><td colspan="7" class="text-center text-muted py-4">加载中...</td></tr>` |
| 70 | `      <span class="modal-title">同步 AWS Bedrock 模型 — 预览与定价确认</span>` |
| 74 | `      <p class="text-muted mb-2">共 <strong id="bedrock-preview-count">0</strong> 个模型。定价单位为 USD / 1M tokens，可直接在表格中修改。</p>` |
| 78 | `            <th>模型</th>` |
| 93 | `        <span>同步后直接启用（正常服务）</span>` |
| 96 | `        <button class="btn btn-secondary" onclick="document.getElementById('bedrock-preview-modal').style.display='none'">取消</button>` |
| 97 | `        <button class="btn btn-primary" onclick="submitBedrockSync()">确认同步</button>` |
| 107 | `      <span class="modal-title">编辑 AWS 模型定价</span>` |
| 119 | `          <label class="form-label">API模型名称</label>` |
| 124 | `            <label class="form-label">输入-缓存命中 ($/1M)</label>` |
| 128 | `            <label class="form-label">输入-缓存未命中 ($/1M)</label>` |
| 134 | `            <label class="form-label">输出 ($/1M)</label>` |
| 138 | `            <label class="form-label">推理 ($/1M)</label>` |
| 143 | `          <button type="button" class="btn btn-secondary" onclick="document.getElementById('edit-aws-modal').style.display='none'">取消</button>` |
| 144 | `          <button type="submit" class="btn btn-primary">保存</button>` |
| 170 | `  showMessage('成功', message);` |
| 174 | `  showMessage('错误', message);` |
| 181 | `  tbody.innerHTML = '<tr><td colspan="7" class="text-center text-muted py-4">加载中...</td></tr>';` |
| 188 | `        tbody.innerHTML = '<tr><td colspan="7" class="text-center text-muted py-4">暂无 AWS 模型，请点击"同步 AWS Bedrock"导入</td></tr>';` |
| 194 | `      tbody.innerHTML = '<tr><td colspan="7" class="text-center text-danger py-4">加载失败: ' + err.message + '</td></tr>';` |
| 203 | `    if (m.status === 'active') statusBadge = '<span class="badge badge-success">正常</span>';` |
| 204 | `    else if (m.status === 'pending') statusBadge = '<span class="badge badge-info">待确认</span>';` |
| 205 | `    else if (m.status === 'paused') statusBadge = '<span class="badge badge-warning">已暂停</span>';` |
| 206 | `    else if (m.status === 'eol_pending') statusBadge = '<span class="badge badge-orange">EOL预告</span>';` |
| 208 | `    else if (m.status === 'discontinued') statusBadge = '<span class="badge badge-muted">已下架</span>';` |
| 209 | `    else if (m.status === 'ignored') statusBadge = '<span class="badge badge-muted">已忽略</span>';` |
| 214 | `      actions = '<button class="btn btn-sm btn-secondary" onclick="pauseAWSModel(\'' + m.id + '\')">暂停</button>';` |
| 216 | `      actions = '<button class="btn btn-sm btn-success" onclick="resumeAWSModel(\'' + m.id + '\')">恢复</button>';` |
| 218 | `      actions = '<button class="btn btn-sm btn-success" onclick="confirmAWSModel(\'' + m.id + '\')">确认</button>';` |
| 220 | `    actions += ' <button class="btn btn-sm btn-ghost" onclick="editAWSModel(\'' + m.id + '\')">编辑</button>';` |
| 275 | `        showSuccess('保存成功');` |
| 278 | `        showError('保存失败: ' + JSON.stringify(data.error));` |
| 281 | `    .catch(e => showError('保存失败: ' + e.message));` |
| 285 | `  showConfirm('暂停模型', '确定要暂停此 AWS 模型吗？', function() {` |
| 299 | `  showConfirm('确认模型', '确认后模型将变为"正常服务"状态', function() {` |
| 319 | `        showError('获取预览失败: ' + (data.error.message \|\| JSON.stringify(data.error)));` |
| 326 | `    .catch(e => showError('获取预览失败: ' + e.message));` |
| 368 | `        showError('同步失败: ' + (data.error.message \|\| JSON.stringify(data.error)));` |
| 370 | `        showSuccess('Bedrock 同步成功! 新增 ' + (data.added \|\| 0) + ' 个, 更新 ' + (data.updated \|\| 0) + ' 个');` |
| 376 | `      showError('同步失败: ' + e.message);` |

## admin-models-general.html (220 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <h1 class="card-title">模型管理-通用</h1>` |
| 8 | `        <option value="">全部状态</option>` |
| 9 | `        <option value="pending">待确认</option>` |
| 10 | `        <option value="active">正常服务</option>` |
| 11 | `        <option value="paused">已暂停</option>` |
| 12 | `        <option value="eol_pending">EOL预告</option>` |
| 13 | `        <option value="eol">EOL服务中</option>` |
| 14 | `        <option value="discontinued">已下架</option>` |
| 15 | `        <option value="ignored">已忽略</option>` |
| 18 | `        <option value="">所有供应商</option>` |
| 23 | `      <button class="btn btn-primary" onclick="showAddModelModal()">手动添加模型</button>` |
| 24 | `      <button class="btn btn-secondary" onclick="resyncServiceModelsToKeys()">手动模型同步</button>` |
| 31 | `      <div class="stat-label">已同步模型</div>` |
| 35 | `      <div class="stat-label">正常服务</div>` |
| 39 | `      <div class="stat-label">待确认</div>` |
| 43 | `      <div class="stat-label">已暂停</div>` |
| 47 | `      <div class="stat-label">EOL预告/服务中</div>` |
| 51 | `      <div class="stat-label">已下架</div>` |
| 61 | `          <th>API模型名称</th>` |
| 63 | `          <th style="text-align:right">输入(缓存命中)</th>` |
| 64 | `          <th style="text-align:right">输入(缓存未命中)</th>` |
| 65 | `          <th style="text-align:right">输出</th>` |
| 66 | `          <th style="text-align:right">推理</th>` |
| 67 | `          <th>状态</th>` |
| 82 | `              {{if eq .Status "active"}}正常{{else if eq .Status "pending"}}待确认{{else if eq .Status "paused"}}已暂停{{else if eq .Status "eol_pending"}}EOL` |
| 84 | `            {{if and (eq .Status "discontinued") .Hidden}}<span class="badge badge-muted" style="margin-left:4px">已隐藏</span>{{end}}` |
| 89 | `              <button class="btn btn-sm btn-success" onclick="confirmModel('{{.ID}}', '{{.ModelID}}', '{{.Name}}')">确认</button>` |
| 90 | `              <button class="btn btn-sm btn-secondary" onclick="editModel('{{.ID}}')">编辑</button>` |
| 91 | `              <button class="btn btn-sm btn-ghost" onclick="ignoreModel('{{.ID}}')">忽略</button>` |
| 93 | `              <button class="btn btn-sm btn-ghost" onclick="editModel('{{.ID}}')">编辑</button>` |
| 94 | `              <button class="btn btn-sm btn-secondary" onclick="pauseModel('{{.ID}}')">暂停</button>` |
| 95 | `              <button class="btn btn-sm btn-warning" onclick="showEOLPendingModal('{{.ID}}')">EOL预告</button>` |
| 97 | `              <button class="btn btn-sm btn-success" onclick="resumeModel('{{.ID}}')">恢复</button>` |
| 98 | `              <button class="btn btn-sm btn-ghost" onclick="editModel('{{.ID}}')">编辑</button>` |
| 99 | `              <button class="btn btn-sm btn-warning" onclick="showEOLPendingModal('{{.ID}}')">EOL预告</button>` |
| 101 | `              <button class="btn btn-sm btn-success" onclick="restoreModel('{{.ID}}')">恢复</button>` |
| 102 | `              <button class="btn btn-sm btn-danger" onclick="deleteModel('{{.ID}}')">删除</button>` |
| 104 | `              <button class="btn btn-sm btn-ghost" onclick="showEditEOLModal('{{.ID}}')">编辑EOL</button>` |
| 105 | `              <button class="btn btn-sm btn-danger" onclick="confirmEOL('{{.ID}}')">确认EOL</button>` |
| 106 | `              <button class="btn btn-sm btn-secondary" onclick="cancelEOL('{{.ID}}')">取消EOL</button>` |
| 108 | `              <button class="btn btn-sm btn-ghost" onclick="showEditEOLModal('{{.ID}}')">编辑EOL</button>` |
| 109 | `              <button class="btn btn-sm btn-danger" onclick="discontinueModel('{{.ID}}')">下架</button>` |
| 111 | `              <button class="btn btn-sm btn-ghost" onclick="toggleHidden('{{.ID}}', {{if .Hidden}}false{{else}}true{{end}})">{{if .Hidden}}显示{{else}}隐` |
| 122 | `    <span class="pagination-info">共 {{$d.Total}} 条，第 {{$d.Page}} / {{$d.TotalPages}} 页</span>` |
| 124 | `    <a class="btn btn-sm btn-secondary" href="/admin/models?page={{int (sub $d.Page 1)}}">上一页</a>` |
| 127 | `    <a class="btn btn-sm btn-secondary" href="/admin/models?page={{int (add $d.Page 1)}}">下一页</a>` |
| 132 | `  <p class="text-muted text-center mt-4">暂无模型，请从 OpenRouter 添加或手动添加模型</p>` |
| 139 | `    <h2 class="card-title">OpenRouter模型同步</h2>` |
| 141 | `      <button class="btn btn-secondary" onclick="showOpenRouterModal()">从 OpenRouter 添加</button>` |
| 142 | `      <button class="btn btn-secondary" onclick="syncOpenRouter()">同步 OpenRouter</button>` |
| 145 | `  <p class="text-muted text-sm mt-2 mb-4">从 OpenRouter API 同步模型列表，并将选中模型添加到服务模型库。</p>` |
| 151 | `    <h2 class="card-title">Provider 模型同步</h2>` |
| 154 | `        <option value="">选择 Provider</option>` |
| 159 | `      <button class="btn btn-primary" onclick="fetchProviderModels()">获取模型列表</button>` |
| 162 | `  <p class="text-muted text-sm mt-2 mb-4">从 Provider 的 /v1/models API 获取支持的模型列表，可一键同步到该 Provider 的所有上游 Key。</p>` |
| 167 | `        <button class="btn btn-primary" onclick="addProviderModelsToService()">添加选中到服务模型</button>` |
| 168 | `        <button class="btn btn-secondary" onclick="showSyncConfirmModal()">同步选中模型到上行Key</button>` |
| 176 | `            <th>模型名称 (product_id)</th>` |
| 177 | `            <th>已映射</th>` |
| 190 | `    <h2 class="card-title">模型路由配置</h2>` |
| 193 | `        <option value="">所有模型</option>` |
| 195 | `      <button class="btn btn-primary" onclick="showAddRoutingModal()">添加路由规则</button>` |
| 198 | `  <p class="text-muted text-sm mt-2 mb-4">配置同一模型在不同 Provider 之间的服务优先级。数值越小优先级越高，Proxy 按此顺序尝试 Provider。</p>` |
| 209 | `        <p class="text-sm font-medium" style="color: var(--text);">路由工作模式说明</p>` |
| 211 | `          <li><strong>VIP / Premium 用户</strong>：路由优先选择 AWS Bedrock（amazon）Provider，确保高可用性和低延迟。</li>` |
| 212 | `          <li><strong>普通用户</strong>：按下方<strong>模型路由配置</strong>规则执行，Proxy 按优先级顺序尝试各 Provider。</li>` |
| 213 | `          <li><strong>未配置路由的模型</strong>：自动启用<strong>智能路由</strong>（Smart Routing），根据健康状态、延迟、配额等动态选择最优 Provider。</li>` |
| 222 | `      <h3 style="font-size:14px; font-weight:600;">智能路由 (Smart Routing) <span style="font-size:12px; color:var(--text-muted); font-weight:400;">— 未手动配` |
| 227 | `          <input type="checkbox" id="smart-health" checked onchange="toggleSmartRule('health', this.checked)"> 健康过滤` |
| 230 | `          <input type="checkbox" id="smart-tier" checked onchange="toggleSmartRule('tier', this.checked)"> 级别优先` |
| 233 | `          <input type="checkbox" id="smart-rpm" checked onchange="toggleSmartRule('rpm', this.checked)"> RPM容限` |
| 236 | `          <input type="checkbox" id="smart-latency" checked onchange="toggleSmartRule('latency', this.checked)"> 延迟优先` |
| 239 | `          <input type="checkbox" id="smart-quota" checked onchange="toggleSmartRule('quota', this.checked)"> 配额优先` |
| 242 | `      <p class="text-xs text-muted mt-2">提示：手动路由配置（下表）优先级高于智能路由。智能路由仅在未手动配置的模型上生效。</p>` |
| 250 | `          <th>模型名称</th>` |
| 252 | `          <th>优先级</th>` |
| 253 | `          <th>状态</th>` |
| 254 | `          <th>操作</th>` |
| 258 | `        <tr><td colspan="5" class="text-center text-muted py-4">加载中...</td></tr>` |
| 268 | `      <span class="modal-title">手动添加模型</span>` |
| 276 | `            <label class="form-label">供应商 *</label>` |
| 278 | `              <option value="">选择供应商</option>` |
| 285 | `            <label class="form-label">产品ID *</label>` |
| 290 | `          <label class="form-label">API模型名称 (api_model_name)</label>` |
| 291 | `          <input type="text" name="api_model_name" class="form-input" placeholder="小写+数字+连字符，如 claude-opus-4-6">` |
| 295 | `            <label class="form-label">输入-缓存命中 ($/1M)</label>` |
| 299 | `            <label class="form-label">输入-缓存未命中 ($/1M) *</label>` |
| 305 | `            <label class="form-label">输出 ($/1M) *</label>` |
| 309 | `            <label class="form-label">推理 ($/1M)</label>` |
| 315 | `          <button type="submit" class="btn btn-primary">添加</button>` |
| 326 | `      <span class="modal-title">编辑模型</span>` |
| 334 | `          <label class="form-label">产品ID (product_id)</label>` |
| 336 | `          <small class="text-muted">Provider 端识别的模型名称（如 MiniMax-M2.7、claude-opus-4-6-20251101）</small>` |
| 339 | `          <label class="form-label">API模型名称 (api_model_name)</label>` |
| 341 | `          <small class="text-muted">用户请求时使用的统一模型名，用于路由匹配（小写+数字+连字符，如 claude-opus-4-6）</small>` |
| 345 | `            <label class="form-label">输入-缓存命中 ($/1M)</label>` |
| 349 | `            <label class="form-label">输入-缓存未命中 ($/1M)</label>` |
| 355 | `            <label class="form-label">输出 ($/1M)</label>` |
| 359 | `            <label class="form-label">推理 ($/1M)</label>` |
| 365 | `          <button type="submit" class="btn btn-primary">保存</button>` |
| 376 | `      <span class="modal-title">确认模型</span>` |
| 383 | `        <p class="text-muted mb-4">确认模型后将变为"正常服务"状态，可在商店中显示。</p>` |
| 385 | `          <label class="form-label">API模型名称</label>` |
| 390 | `            <label class="form-label">输入-缓存命中 ($/1M)</label>` |
| 394 | `            <label class="form-label">输入-缓存未命中 ($/1M) *</label>` |
| 400 | `            <label class="form-label">输出 ($/1M) *</label>` |
| 404 | `            <label class="form-label">推理 ($/1M)</label>` |
| 410 | `          <button type="submit" class="btn btn-success">确认启用</button>` |
| 421 | `      <span class="modal-title">设置 EOL 预告</span>` |
| 428 | `        <p class="text-muted mb-4">EOL 预告后：不可新购买、继续服务现有用户、商店显示"即将下线"</p>` |
| 430 | `          <label class="form-label">计划停止服务日期</label>` |
| 432 | `          <small class="text-muted">留空则由供应商通知时填写</small>` |
| 435 | `          <label class="form-label">EOL 备注</label>` |
| 436 | `          <textarea name="eol_notes" id="eol-pending-notes" class="form-input" rows="3" placeholder="如：供应商通知链接、下线原因等"></textarea>` |
| 440 | `          <button type="submit" class="btn btn-warning">设置 EOL 预告</button>` |
| 451 | `      <span class="modal-title">编辑 EOL 信息</span>` |
| 459 | `          <label class="form-label">宣布 EOL 时间</label>` |
| 463 | `          <label class="form-label">计划停止服务时间</label>` |
| 467 | `          <label class="form-label">计划清除时间</label>` |
| 469 | `          <small class="text-muted">通常为停止服务后 1 个月</small>` |
| 472 | `          <label class="form-label">EOL 备注</label>` |
| 477 | `          <button type="submit" class="btn btn-primary">保存</button>` |
| 488 | `      <span class="modal-title">从 OpenRouter 添加模型</span>` |
| 494 | `          <option value="">所有供应商</option>` |
| 499 | `        <input type="text" id="or-search" class="form-input" style="width: 200px" placeholder="搜索模型..." onkeyup="debounceLoadOR()">` |
| 500 | `        <button class="btn btn-primary" onclick="addSelectedModels()">添加选中模型</button>` |
| 517 | `// 显示成功消息` |
| 519 | `  showMessage('成功', message);` |
| 522 | `// 显示错误消息` |
| 524 | `  showMessage('错误', message);` |
| 527 | `// ========== 筛选函数 ==========` |
| 539 | `    // Hide discontinued+hidden models by default, unless explicitly filtering for "已下架"` |
| 553 | `  showConfirm('同步 OpenRouter', '确定要从 OpenRouter API 同步模型吗？这将更新本地缓存的模型列表。', function() {` |
| 561 | `          showError('同步失败: ' + (data.error.message \|\| JSON.stringify(data.error)));` |
| 563 | `          showSuccess('OpenRouter 同步成功! 已同步 ' + (data.synced \|\| 0) + ' 个模型, 跳过 ' + (data.skipped \|\| 0) + ' 个');` |
| 567 | `      .catch(e => showError('同步失败: ' + e.message));` |
| 572 | `  showConfirm('手动模型同步', '确定要重新同步所有活跃服务模型到上行Key吗？', function() {` |
| 580 | `          showSuccess('同步成功! 已更新 ' + (data.keys_updated \|\| 0) + ' 个 Key');` |
| 582 | `          showError('同步失败: ' + (data.message \|\| '未知错误'));` |
| 585 | `      .catch(e => showError('同步失败: ' + e.message));` |
| 615 | `        showError('添加失败: ' + (data.error.message \|\| JSON.stringify(data.error)));` |
| 617 | `        showSuccess('模型添加成功，已生效');` |
| 621 | `    .catch(e => showError('添加失败: ' + e.message));` |
| 666 | `        showError('保存失败: ' + JSON.stringify(data.error));` |
| 669 | `    .catch(e => showError('保存失败: ' + e.message));` |
| 704 | `        showError('确认失败: ' + JSON.stringify(data.error));` |
| 707 | `    .catch(e => showError('确认失败: ' + e.message));` |
| 711 | `  showConfirm('暂停模型', '确定要暂停此模型吗？暂停后模型将不可用。', function() {` |
| 716 | `        else showError('暂停失败: ' + JSON.stringify(data.error));` |
| 726 | `      else showError('恢复失败: ' + JSON.stringify(data.error));` |
| 731 | `  showConfirm('忽略模型', '确定要忽略此模型吗？忽略后可在需要时恢复。', function() {` |
| 736 | `        else showError('忽略失败: ' + JSON.stringify(data.error));` |
| 742 | `  showConfirm('下架模型', '确定要下架此模型吗？此操作不可恢复，模型将完全停止服务。', function() {` |
| 747 | `        else showError('下架失败: ' + JSON.stringify(data.error));` |
| 753 | `  var action = hidden ? '隐藏' : '显示';` |
| 754 | `  showConfirm(action + '模型', '确定要' + action + '此模型吗？', function() {` |
| 763 | `        else showError(action + '失败: ' + JSON.stringify(data.error));` |
| 769 | `  showConfirm('删除模型', '确定要删除此模型吗？此操作不可恢复，数据将完全移除。', function() {` |
| 774 | `        else showError('删除失败: ' + JSON.stringify(data.error));` |
| 781 | `  showConfirm('恢复模型', '确定要恢复此模型吗？模型将变为"待确认"状态。', function() {` |
| 790 | `        else showError('恢复失败: ' + JSON.stringify(data.error));` |
| 821 | `      else showError('设置失败: ' + JSON.stringify(data.error));` |
| 859 | `      else showError('保存失败: ' + JSON.stringify(data.error));` |
| 865 | `  showConfirm('确认 EOL', '确定要确认EOL吗？确认后模型将进入EOL服务状态：不可购买、继续服务现有用户（最多1个月）、商店不显示。', function() {` |
| 870 | `        else showError('确认失败: ' + JSON.stringify(data.error));` |
| 877 | `  showConfirm('取消 EOL 预告', '确定要取消EOL预告吗？模型将恢复正常服务。', function() {` |
| 882 | `        else showError('取消失败: ' + JSON.stringify(data.error));` |
| 918 | `  var html = '<table class="data-table"><thead><tr><th><input type="checkbox" onchange="toggleAllOR(this)"></th><th>模型ID</th><th>供应商</th><th>名称</th><t` |
| 933 | `    html = '<p class="text-muted text-center">没有找到模型</p>';` |
| 949 | `    showMessage('提示', '请选择要添加的模型');` |
| 956 | `  showConfirm('添加模型', '确定要添加 ' + ids.length + ' 个模型吗？', function() {` |
| 966 | `            var reason = (data.errors && data.errors.length > 0) ? data.errors.join('\n') : '未知原因';` |
| 967 | `            showMessage('未添加任何模型', reason);` |
| 969 | `            showSuccess('添加成功! 已添加 ' + data.added + ' 个模型, 跳过 ' + data.skipped + ' 个');` |
| 975 | `          showMessage('添加失败', msg);` |
| 978 | `      .catch(e => showError('添加失败: ' + e.message));` |
| 1013 | `      select.innerHTML = '<option value="">所有模型</option>';` |
| 1042 | `    tbody.innerHTML = '<tr><td colspan="5" class="text-center text-muted py-4">暂无路由规则</td></tr>';` |
| 1049 | `      ? '<span class="badge badge-success">启用</span>'` |
| 1050 | `      : '<span class="badge badge-error">禁用</span>';` |
| 1057 | `      '<button class="btn btn-sm btn-secondary" onclick="toggleRoutingRule(\'' + rule.id + '\', ' + !rule.enabled + ')">' + (rule.enabled ? '禁用' : '启用` |
| 1058 | `      '<button class="btn btn-sm btn-danger" onclick="deleteRoutingRule(\'' + rule.id + '\')">删除</button>' +` |
| 1117 | `    showMessage('提示', '模型名称和 Provider 必填');` |
| 1131 | `      showMessage('添加成功', '路由规则已添加');` |
| 1133 | `      showMessage('添加失败', (data.error && data.error.message) \|\| '请重试');` |
| 1151 | `  showConfirm('删除路由规则', '确定删除此规则？', function() {` |
| 1165 | `      <span class="modal-title">添加路由规则</span>` |
| 1172 | `          <label class="form-label">模型名称 *</label>` |
| 1174 | `            <option value="">选择模型</option>` |
| 1180 | `            <option value="">选择 Provider</option>` |
| 1187 | `          <label class="form-label">优先级</label>` |
| 1189 | `          <small class="text-muted">数值越小优先级越高</small>` |
| 1193 | `          <button type="submit" class="btn btn-primary">添加</button>` |
| 1215 | `        select.innerHTML = '<option value="">选择模型</option>';` |
| 1234 | `  if (!provider) { showMessage('错误', '请选择 Provider'); return; }` |
| 1238 | `  tbody.innerHTML = '<tr><td colspan="4" class="text-center text-muted py-4">加载中...</td></tr>';` |
| 1257 | `      if (data.error) throw new Error(data.error.message \|\| '未知错误');` |
| 1261 | `        '共 ' + providerModelsData.length + ' 个模型' + (hint ? ' — ' + hint : '');` |
| 1264 | `        var emptyMsg = hint \|\| '无模型';` |
| 1271 | `        var mapped = m.already_mapped ? '<span class="badge badge-success">已映射</span>' : '<span class="badge badge-secondary">未映射</span>';` |
| 1288 | `      tbody.innerHTML = '<tr><td colspan="4" class="text-center text-danger py-4">获取失败: ' + err.message + '</td></tr>';` |
| 1308 | `  if (!provider) { showMessage('错误', '请选择 Provider'); return; }` |
| 1311 | `  if (selected.length === 0) { showMessage('错误', '请选择要添加的模型'); return; }` |
| 1313 | `  showConfirm('添加到服务模型', '将 ' + selected.length + ' 个模型添加到服务模型表？\n多段 product_id（如 Pro/moonshotai/Kimi-K2.6）会自动生成简化 api_model_name（如 kimi-k2.6）。', func` |
| 1322 | `          var msg = '添加成功! 已添加 ' + data.added + ' 个模型, 跳过 ' + data.skipped + ' 个（已存在）';` |
| 1323 | `          if (data.errors && data.errors.length > 0) msg += '\n错误: ' + data.errors.join(', ');` |
| 1324 | `          showMessage('添加结果', msg);` |
| 1328 | `          showError('添加失败: ' + (data.error && data.error.message \|\| '未知错误'));` |
| 1331 | `      .catch(function(err) { showError('添加失败: ' + err.message); });` |
| 1337 | `  if (!provider) { showMessage('错误', '请选择 Provider'); return; }` |
| 1345 | `  if (selected.length === 0) { showMessage('错误', '请选择要同步的模型'); return; }` |
| 1370 | `        document.getElementById('sync-result-modal-msg').textContent = '同步成功！已更新 ' + data.keys_updated + ' 个上行Key，共 ' + data.models_count + ' 个模型';` |
| 1373 | `        document.getElementById('sync-result-modal-msg').textContent = '同步失败: ' + (data.error && data.error.message \|\| '未知错误');` |
| 1378 | `      document.getElementById('sync-result-modal-msg').textContent = '请求失败: ' + err.message;` |
| 1389 | `      <span class="modal-title">确认同步</span>` |
| 1393 | `      <p>确认将 <strong id="sync-confirm-count">0</strong> 个模型同步到 <strong id="sync-confirm-provider"></strong> 的所有上行Key？</p>` |
| 1396 | `      <button class="btn btn-secondary" onclick="document.getElementById('sync-confirm-modal').style.display='none'">取消</button>` |
| 1397 | `      <button class="btn btn-primary" onclick="doSyncModels()">确认同步</button>` |
| 1406 | `      <span class="modal-title">同步结果</span>` |
| 1413 | `      <button class="btn btn-primary" onclick="document.getElementById('sync-result-modal').style.display='none'">确定</button>` |

## admin-monitor-downstream.html (34 处)

| 行号 | 代码 |
|------|------|
| 6 | `    <p class="text-muted">监控用户API密钥使用情况和状态</p>` |
| 11 | `      刷新` |
| 23 | `      <div class="stat-label">总密钥数</div>` |
| 32 | `      <div class="stat-label">活跃密钥</div>` |
| 41 | `      <div class="stat-label">总请求数</div>` |
| 50 | `      <div class="stat-label">总Token数</div>` |
| 59 | `    <h2 class="card-title">密钥列表</h2>` |
| 61 | `      <input type="text" id="searchInput" placeholder="搜索用户/密钥名..." onkeyup="filterKeys()" class="form-input form-input-sm" style="width:200px">` |
| 63 | `        <option value="">全部状态</option>` |
| 64 | `        <option value="active">活跃</option>` |
| 65 | `        <option value="disabled">禁用</option>` |
| 68 | `        <option value="">全部类型</option>` |
| 69 | `        <option value="general">标准</option>` |
| 70 | `        <option value="subscription">套餐</option>` |
| 71 | `        <option value="enterprise">企业</option>` |
| 79 | `          <th>密钥名称</th>` |
| 80 | `          <th>前缀</th>` |
| 81 | `          <th>用户</th>` |
| 82 | `          <th>类型</th>` |
| 83 | `          <th>用户组</th>` |
| 84 | `          <th>当前模型</th>` |
| 85 | `          <th>请求数</th>` |
| 86 | `          <th>Token数</th>` |
| 87 | `          <th>状态</th>` |
| 88 | `          <th>创建时间</th>` |
| 95 | `          <td>{{if .Name}}{{.Name}}{{else}}<span class="text-muted">未命名</span>{{end}}</td>` |
| 100 | `              标准` |
| 102 | `              套餐` |
| 104 | `              企业` |
| 115 | `              <span class="badge badge-success">活跃</span>` |
| 117 | `              <span class="badge badge-secondary">禁用</span>` |
| 129 | `    <span class="pagination-info">共 {{$d.Total}} 条，第 {{$d.Page}} / {{$d.TotalPages}} 页</span>` |
| 131 | `    <a class="btn btn-sm btn-secondary" href="/admin/monitor-downstream?page={{int (sub $d.Page 1)}}">上一页</a>` |
| 134 | `    <a class="btn btn-sm btn-secondary" href="/admin/monitor-downstream?page={{int (add $d.Page 1)}}">下一页</a>` |

## admin-monitor-upstream.html (29 处)

| 行号 | 代码 |
|------|------|
| 6 | `    <p class="text-muted">监控上游API密钥状态和健康度</p>` |
| 11 | `      刷新` |
| 23 | `      <div class="stat-label">总密钥数</div>` |
| 32 | `      <div class="stat-label">活跃密钥</div>` |
| 41 | `      <div class="stat-label">失败密钥</div>` |
| 50 | `      <div class="stat-label">待验证</div>` |
| 59 | `    <h2 class="card-title">密钥列表</h2>` |
| 62 | `        <option value="">全部供应商</option>` |
| 70 | `        <option value="">全部状态</option>` |
| 71 | `        <option value="valid">正常</option>` |
| 72 | `        <option value="invalid">无效</option>` |
| 73 | `        <option value="failed">失败</option>` |
| 74 | `        <option value="pending">待验证</option>` |
| 82 | `          <th>供应商</th>` |
| 83 | `          <th>密钥预览</th>` |
| 84 | `          <th>状态</th>` |
| 85 | `          <th>状态</th>` |
| 86 | `          <th>最后验证</th>` |
| 87 | `          <th>创建时间</th>` |
| 98 | `              <span class="badge badge-success">正常</span>` |
| 100 | `              <span class="badge badge-danger">无效</span>` |
| 102 | `              <span class="badge badge-danger">失败</span>` |
| 104 | `              <span class="badge badge-warning">待验证</span>` |
| 111 | `              <span class="badge badge-success">活跃</span>` |
| 113 | `              <span class="badge badge-secondary">禁用</span>` |
| 120 | `              <span class="text-muted">未验证</span>` |
| 132 | `    <span class="pagination-info">共 {{$d.Total}} 条，第 {{$d.Page}} / {{$d.TotalPages}} 页</span>` |
| 134 | `    <a class="btn btn-sm btn-secondary" href="/admin/monitor-upstream?page={{int (sub $d.Page 1)}}">上一页</a>` |
| 137 | `    <a class="btn btn-sm btn-secondary" href="/admin/monitor-upstream?page={{int (add $d.Page 1)}}">下一页</a>` |

## admin-provider-health.html (44 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <p class="text-muted">基于真实上游流量自动计算，5分钟窗口聚合</p>` |
| 10 | `      刷新` |
| 14 | `      导出 CSV` |
| 62 | `    <h2 class="card-title">供应商状态</h2>` |
| 65 | `        <option value="15m">最近 15 分钟</option>` |
| 66 | `        <option value="30m">最近 30 分钟</option>` |
| 67 | `        <option value="1h">最近 1 小时</option>` |
| 68 | `        <option value="12h">最近 12 小时</option>` |
| 69 | `        <option value="24h">最近 24 小时</option>` |
| 72 | `        <option value="">全部状态</option>` |
| 86 | `          <th>状态</th>` |
| 87 | `          <th class="text-right">请求数</th>` |
| 88 | `          <th class="text-right">成功率</th>` |
| 89 | `          <th class="text-right">错误率</th>` |
| 90 | `          <th class="text-right">平均延迟</th>` |
| 91 | `          <th>操作</th>` |
| 95 | `        <tr><td colspan="7" class="text-center text-muted py-8">加载中...</td></tr>` |
| 142 | `        '<tr><td colspan="7" class="text-center text-muted py-8">加载失败: ' + err.message + '</td></tr>';` |
| 149 | `    tbody = '<tr><td colspan="7" class="text-center text-muted py-8">暂无数据</td></tr>';` |
| 156 | `      var disabledBadge = p.enabled ? '' : '<span class="badge badge-secondary">禁用</span>';` |
| 164 | `        '<td><button class="btn btn-sm btn-ghost" onclick="openDetailModal(\'' + p.provider + '\')">详情</button></td>' +` |
| 218 | `        '当前状态基于列表页选定的时间范围：<strong>' + getWindowLabel() + '</strong>' +` |
| 221 | `        '<div class="stat-card" style="padding:16px"><div class="stat-label">请求数</div><div class="stat-value">' + totalReq + '</div></div>' +` |
| 222 | `        '<div class="stat-card" style="padding:16px"><div class="stat-label">成功率</div><div class="stat-value text-success">' + successRate + '</div></` |
| 223 | `        '<div class="stat-card" style="padding:16px"><div class="stat-label">错误率</div><div class="stat-value text-danger">' + errorRate + '</div></div` |
| 224 | `        '<div class="stat-card" style="padding:16px"><div class="stat-label">平均延迟</div><div class="stat-value">' + latency + '</div></div>' +` |
| 235 | `        tbody = '<tr><td colspan="5" class="text-center text-muted py-4">暂无历史数据</td></tr>';` |
| 255 | `      showMessage('加载失败', '加载详情失败: ' + err.message);` |
| 297 | `          label: '请求数',` |
| 307 | `          label: '延迟(ms)',` |
| 317 | `          label: '错误率(%)',` |
| 356 | `          title: { display: true, text: '请求数', font: { size: 10 } },` |
| 364 | `          title: { display: true, text: '延迟(ms)', font: { size: 10 } },` |
| 372 | `          title: { display: true, text: '错误率(%)', font: { size: 10 } },` |
| 403 | `      <span class="modal-title"><span id="detail-modal-provider"></span> — 详情 <span id="detail-modal-window-label" style="font-size:0.8rem;font-weight` |
| 408 | `        <strong>当前状态：</strong><span id="detail-modal-status"></span>` |
| 411 | `      <h3 style="font-size:0.95rem;font-weight:600;margin:16px 0 4px">近 7 天历史趋势</h3>` |
| 412 | `      <div style="font-size:0.75rem;color:var(--text-muted);margin-bottom:8px">小时级聚合 · 与列表页时间窗口无关</div>` |
| 420 | `              <th>时间</th>` |
| 421 | `              <th class="text-right">请求数</th>` |
| 422 | `              <th class="text-right">成功 / 失败</th>` |
| 423 | `              <th class="text-right">平均延迟</th>` |
| 424 | `              <th>状态</th>` |
| 432 | `      <button class="btn btn-secondary" onclick="closeDetailModal()">关闭</button>` |

## admin-providers.html (45 处)

| 行号 | 代码 |
|------|------|
| 7 | `      <input type="text" id="provider-search" class="form-input" placeholder="搜索供应商..." style="width: 200px" onkeyup="filterProviders()">` |
| 9 | `        <option value="">所有状态</option>` |
| 13 | `      <button class="btn btn-primary" onclick="showAddModal()">添加供应商</button>` |
| 20 | `      <div class="stat-label">总供应商</div>` |
| 24 | `      <div class="stat-label">已启用</div>` |
| 28 | `      <div class="stat-label">禁用</div>` |
| 32 | `      <div class="stat-label">活跃密钥</div>` |
| 43 | `          <th>名称</th>` |
| 44 | `          <th>类型</th>` |
| 46 | `          <th>密钥数</th>` |
| 47 | `          <th>模型数</th>` |
| 48 | `          <th>状态</th>` |
| 49 | `          <th>操作</th>` |
| 64 | `            <code class="font-mono text-xs">{{if .BaseURL}}{{.BaseURL}}{{else}}默认{{end}}</code>` |
| 72 | `              {{if .Enabled}}活跃{{else}}禁用{{end}}` |
| 77 | `              <button class="btn btn-sm btn-ghost" onclick="editProvider('{{.ID}}')" title="编辑">` |
| 80 | `              <button class="btn btn-sm btn-secondary" onclick="toggleProvider('{{.ID}}', {{if .Enabled}}false{{else}}true{{end}})">{{if .Enabled}}禁用{` |
| 89 | `  <p class="text-muted text-center mt-4">暂无供应商</p>` |
| 97 | `      <span class="modal-title">添加供应商</span>` |
| 105 | `          <input type="text" name="id" id="add-provider-id" class="form-input" required placeholder="openai" pattern="[a-z0-9-]+" title="只能使用小写字母、数字和连` |
| 106 | `          <p class="text-xs text-muted mt-1">唯一标识符，如 openai, anthropic, deepseek</p>` |
| 109 | `          <label class="form-label">名称</label>` |
| 113 | `          <label class="form-label">类型</label>` |
| 115 | `            <option value="">选择类型</option>` |
| 116 | `            <option value="international">国际</option>` |
| 117 | `            <option value="domestic">国内</option>` |
| 123 | `          <p class="text-xs text-muted mt-1">留空使用默认URL</p>` |
| 127 | `          <button type="submit" class="btn btn-primary">添加</button>` |
| 138 | `      <span class="modal-title">编辑供应商</span>` |
| 146 | `          <label class="form-label">名称</label>` |
| 150 | `          <label class="form-label">类型</label>` |
| 152 | `            <option value="international">国际</option>` |
| 153 | `            <option value="domestic">国内</option>` |
| 162 | `          <button type="submit" class="btn btn-primary">保存</button>` |
| 211 | `    showMessage('错误', '无法找到供应商数据');` |
| 238 | `        showMessage('添加失败', data.error.message \|\| JSON.stringify(data.error));` |
| 240 | `        showMessage('添加成功', '供应商已成功添加', function() { window.location.reload(); });` |
| 245 | `      showMessage('添加失败', err.message);` |
| 265 | `        showMessage('编辑失败', data.error.message \|\| JSON.stringify(data.error));` |
| 267 | `        showMessage('编辑成功', '供应商信息已更新', function() { window.location.reload(); });` |
| 272 | `      showMessage('编辑失败', err.message);` |
| 277 | `  var action = enable ? '启用' : '禁用';` |
| 286 | `        showMessage('操作失败', data.error.message \|\| JSON.stringify(data.error));` |
| 288 | `        showMessage('操作成功', '供应商已' + action, function() { window.location.reload(); });` |
| 291 | `    .catch(err => showMessage('操作失败', err.message));` |

## admin-redemption.html (78 处)

| 行号 | 代码 |
|------|------|
| 8 | `        <option value="all">所有类型</option>` |
| 9 | `        <option value="balance">余额兑换</option>` |
| 10 | `        <option value="promo">促销优惠</option>` |
| 15 | `        <option value="all">所有状态</option>` |
| 17 | `        <option value="disabled">禁用</option>` |
| 18 | `        <option value="expired">已过期</option>` |
| 19 | `        <option value="exhausted">已用完</option>` |
| 21 | `      <button class="btn btn-primary" onclick="document.getElementById('create-code-modal').style.display='flex'">创建兑换码</button>` |
| 22 | `      <button class="btn btn-secondary" onclick="document.getElementById('batch-code-modal').style.display='flex'">批量生成</button>` |
| 29 | `      <div class="stat-label">总兑换码</div>` |
| 33 | `      <div class="stat-label">活跃</div>` |
| 37 | `      <div class="stat-label">促销码</div>` |
| 41 | `      <div class="stat-label">已过期</div>` |
| 50 | `          <th>名称</th>` |
| 51 | `          <th>兑换码</th>` |
| 52 | `          <th>类型</th>` |
| 53 | `          <th>金额/计划</th>` |
| 54 | `          <th>使用次数</th>` |
| 56 | `          <th>过期时间</th>` |
| 57 | `          <th>备注</th>` |
| 62 | `        <tr><td colspan="9" class="text-center text-muted py-4">加载中...</td></tr>` |
| 68 | `    <button class="btn btn-sm btn-secondary" id="codes-prev-btn" onclick="codesPage--;loadCodes()" disabled>上一页</button>` |
| 69 | `    <button class="btn btn-sm btn-secondary" id="codes-next-btn" onclick="codesPage++;loadCodes()">下一页</button>` |
| 77 | `      <span class="modal-title">创建兑换码</span>` |
| 84 | `          <label class="form-label">名称</label>` |
| 85 | `          <input type="text" name="name" class="form-input" required placeholder="例如: 新用户优惠">` |
| 88 | `          <label class="form-label">类型</label>` |
| 90 | `            <option value="balance">余额兑换</option>` |
| 91 | `            <option value="promo">促销优惠</option>` |
| 92 | `            <option value="plan">计划兑换</option>` |
| 97 | `            <label class="form-label">金额 ($)</label>` |
| 103 | `            <label class="form-label">选择计划</label>` |
| 105 | `              <option value="">请选择计划</option>` |
| 110 | `            <p class="text-xs text-muted mt-1">选择计划后，兑换码类型将自动匹配计划类型</p>` |
| 114 | `          <label class="form-label">最大使用次数</label>` |
| 116 | `          <p class="text-xs text-muted mt-1">每个兑换码最多可被使用的次数。促销码建议设为多次</p>` |
| 119 | `          <label class="form-label">备注 (可选)</label>` |
| 120 | `          <textarea name="description" class="form-input" rows="2" placeholder="用途说明，方便其他管理员理解"></textarea>` |
| 123 | `          <label class="form-label">过期时间 (可选)</label>` |
| 129 | `        <button type="submit" class="btn btn-primary">创建</button>` |
| 139 | `      <span class="modal-title">批量生成兑换码</span>` |
| 146 | `          <label class="form-label">名称前缀</label>` |
| 147 | `          <input type="text" name="name_prefix" class="form-input" required placeholder="例如: 促销活动">` |
| 150 | `          <label class="form-label">类型</label>` |
| 152 | `            <option value="balance">余额兑换</option>` |
| 153 | `            <option value="promo">促销优惠</option>` |
| 157 | `          <label class="form-label">金额 ($)</label>` |
| 161 | `          <label class="form-label">最大使用次数</label>` |
| 165 | `          <label class="form-label">备注 (可选)</label>` |
| 166 | `          <textarea name="description" class="form-input" rows="2" placeholder="用途说明"></textarea>` |
| 169 | `          <label class="form-label">生成数量</label>` |
| 171 | `          <p class="text-xs text-muted mt-1">最多100个</p>` |
| 174 | `          <label class="form-label">过期时间 (可选)</label>` |
| 180 | `        <button type="submit" class="btn btn-primary">生成</button>` |
| 190 | `      <span class="modal-title">批量生成成功</span>` |
| 194 | `      <p class="text-success mb-2">已生成以下兑换码：</p>` |
| 198 | `      <button class="btn btn-secondary" id="copy-all-btn" onclick="copyAllCodes()">复制全部</button>` |
| 199 | `      <button class="btn btn-primary" onclick="closeBatchResultModal()">确定</button>` |
| 263 | `      list.innerHTML = '<tr><td colspan="9" class="text-center text-muted py-4">暂无兑换码</td></tr>';` |
| 271 | `      var typeLabel = c.type==='balance'?'余额兑换':c.type==='promo'?'促销优惠':c.type==='token_plan'?'Token Plan':'Code Plan';` |
| 272 | `      var statusBadge = c.status==='active'?'<span class="badge badge-success">活跃</span>':` |
| 273 | `                        c.status==='expired'?'<span class="badge badge-warning">已过期</span>':` |
| 274 | `                        c.status==='exhausted'?'<span class="badge badge-info">已用完</span>':` |
| 275 | `                        '<span class="badge badge-error">禁用</span>';` |
| 277 | `      var useCount = c.used_count + '/' + (c.max_uses\|\|'无限');` |
| 278 | `      var expiresAt = c.expires_at ? formatDate(c.expires_at) : '永久';` |
| 282 | `        actions = '<button class="btn btn-sm btn-secondary" onclick="confirmToggleCode(\'/admin/redemption/'+c.id+'/disable\', \'禁用\')">禁用</button>';` |
| 284 | `        actions = '<button class="btn btn-sm btn-success" onclick="confirmToggleCode(\'/admin/redemption/'+c.id+'/enable\', \'启用\')">启用</button>';` |
| 296 | `      document.getElementById('codes-page-info').textContent = '共 ' + data.total + ' 条，第 ' + codesPage + ' / ' + codesTotalPages + ' 页';` |
| 329 | `  showConfirm(action + '兑换码', '确定要' + action + '该兑换码吗？', function() {` |
| 354 | `      showMessage('操作成功', '兑换码创建成功');` |
| 358 | `      showMessage('创建失败', '服务器返回错误 (HTTP ' + r.status + ')，请刷新页面后重试');` |
| 361 | `    showMessage('创建失败', err.message);` |
| 379 | `      showMessage('生成失败', data.error \|\| '未知错误');` |
| 382 | `    showMessage('生成失败', err.message);` |
| 392 | `           '<button class="btn btn-sm btn-ghost" onclick="copySingleCode(\'' + code + '\', this)">复制</button>' +` |
| 417 | `  try { document.execCommand('copy'); showCopySuccess(btn); } catch(e) { showMessage('复制失败', '请手动复制'); }` |
| 422 | `  if (btn) { var orig = btn.textContent; btn.textContent = '已复制'; setTimeout(function() { btn.textContent = orig; }, 1000); }` |

## admin-settings.html (21 处)

| 行号 | 代码 |
|------|------|
| 8 | `    <strong>注意：</strong>以下仅展示<strong>已接入代码</strong>的配置项。修改后立即生效，请谨慎操作。未接入的配置项（如日志保留天数、令牌过期等）暂不展示，待后端实现后再开放。` |
| 12 | `    <div class="text-center py-8 text-muted">加载中...</div>` |
| 127 | `    name: '计费设置',` |
| 130 | `      usd_exchange_rate: { label: '美元汇率', type: 'number', desc: '当前值由系统每 8 小时自动从 API 获取更新。手动修改仅作为临时 fallback，下次自动更新后会被覆盖' }` |
| 134 | `    name: '签到设置',` |
| 137 | `      checkin_enabled: { label: '签到功能', type: 'toggle', desc: '启用每日签到奖励' },` |
| 138 | `      checkin_min_quota: { label: '最小奖励', type: 'number', desc: '签到最小 Token 奖励' },` |
| 139 | `      checkin_max_quota: { label: '最大奖励', type: 'number', desc: '签到最大 Token 奖励' }` |
| 143 | `    name: '敏感词设置',` |
| 146 | `      check_sensitive_enabled: { label: '输出检测', type: 'toggle', desc: '检测 API 响应中的敏感词' },` |
| 147 | `      check_sensitive_on_prompt_enabled: { label: '输入检测', type: 'toggle', desc: '检测用户输入中的敏感词' },` |
| 148 | `      sensitive_words: { label: '敏感词列表', type: 'textarea', desc: '敏感词列表，用逗号分隔' }` |
| 152 | `    name: '路由设置',` |
| 155 | `      routing_strategy: { label: '路由策略', type: 'text', desc: '默认路由策略（priority / round-robin）' },` |
| 156 | `      routing_failover_enabled: { label: '故障转移', type: 'toggle', desc: '启用自动故障转移' },` |
| 157 | `      routing_max_retries: { label: '最大重试', type: 'number', desc: '路由失败后最大重试次数' },` |
| 158 | `      routing_retry_delay_ms: { label: '重试延迟(ms)', type: 'number', desc: '重试之间的延迟毫秒数' }` |
| 183 | `        '<div class="text-center py-8 text-muted">加载失败: ' + err.message + '</div>';` |
| 216 | `      html += '<div class="text-muted text-center py-4">暂无配置</div>';` |
| 257 | `      showMessage('保存失败', result.error \|\| '未知错误');` |
| 262 | `    showMessage('保存失败', err.message);` |

## admin-subscriptions.html (53 处)

| 行号 | 代码 |
|------|------|
| 8 | `        <option value="">所有类型</option>` |
| 13 | `        <option value="">所有状态</option>` |
| 16 | `        <option value="deleted">已删除</option>` |
| 18 | `      <button class="btn btn-primary" onclick="document.getElementById('add-plan-modal').style.display='flex'">创建计划</button>` |
| 25 | `      <div class="stat-label">总计划数</div>` |
| 29 | `      <div class="stat-label">活跃计划</div>` |
| 39 | `          <th>名称</th>` |
| 40 | `          <th>类型</th>` |
| 41 | `          <th>价格</th>` |
| 42 | `          <th>配额</th>` |
| 43 | `          <th>时长</th>` |
| 51 | `          <td><span class="font-medium">{{.Name}}{{if .DeletedAt}} <span class="text-muted text-xs">(已删除)</span>{{end}}</span></td>` |
| 60 | `              {{.Quota}} 次` |
| 65 | `              <span class="text-muted">永久</span>` |
| 67 | `              <span>1 个月</span>` |
| 69 | `              <span>1 年</span>` |
| 71 | `              <span>{{.DurationDays}} 天</span>` |
| 76 | `            <span class="badge badge-error">已删除</span>` |
| 86 | `              <button class="btn btn-sm btn-secondary" onclick="openEditModal('{{.ID}}')">编辑</button>` |
| 88 | `                onclick="confirmTogglePlan('{{.ID}}', '{{.Name}}', '{{if eq .Status "active"}}禁用{{else}}启用{{end}}')">` |
| 89 | `                {{if eq .Status "active"}}禁用{{else}}启用{{end}}` |
| 91 | `              <button class="btn btn-sm btn-error" onclick="confirmDeletePlan('{{.ID}}', '{{.Name}}')">删除</button>` |
| 101 | `  <p class="text-muted text-center mt-4">暂无订阅计划</p>` |
| 109 | `      <span class="modal-title">创建订阅计划</span>` |
| 116 | `          <label class="form-label">名称 *</label>` |
| 117 | `          <input type="text" name="name" class="form-input" required placeholder="基础版">` |
| 120 | `          <label class="form-label">类型 *</label>` |
| 127 | `          <label class="form-label">价格 ($) *</label>` |
| 131 | `          <label class="form-label">配额</label>` |
| 133 | `          <p class="text-xs text-muted mt-1">Token Plan: Token数量 (如 1000000); Code Plan: 使用次数 (如 100)</p>` |
| 136 | `          <label class="form-label">有效期 (天)</label>` |
| 138 | `          <p class="text-xs text-muted mt-1">0 表示永久有效。此为用户购买后额度的使用期限（如 30 天内可用），非购买窗口期</p>` |
| 141 | `          <label class="form-label">每日限额</label>` |
| 143 | `          <p class="text-xs text-muted mt-1">0 表示无限制。Token Plan: 每日Token上限; Code Plan: 每日请求次数上限</p>` |
| 146 | `          <label class="form-label">速率限制</label>` |
| 149 | `              <span class="text-xs text-muted">RPH (请求/时)</span>` |
| 153 | `              <span class="text-xs text-muted">RP5H (请求/5小时)</span>` |
| 157 | `              <span class="text-xs text-muted">RPW (请求/周)</span>` |
| 161 | `              <span class="text-xs text-muted">TPH (Token/时)</span>` |
| 165 | `              <span class="text-xs text-muted">TPD (Token/日)</span>` |
| 169 | `              <span class="text-xs text-muted">TPW (Token/周)</span>` |
| 173 | `              <span class="text-xs text-muted">并发数</span>` |
| 177 | `          <p class="text-xs text-muted mt-1">0 表示无限制。Code Plan 使用 RPH/RP5H/RPW; Token Plan 使用 TPH/TPD/TPW</p>` |
| 182 | `        <button type="submit" class="btn btn-primary">创建</button>` |
| 192 | `      <span class="modal-title">编辑计划</span>` |
| 244 | `  showConfirm(action + '计划', '确定要' + action + '计划 "' + planName + '" 吗？', function() {` |
| 275 | `      document.getElementById('edit-plan-body').innerHTML = '<div class="text-center py-4 text-muted">加载失败</div>';` |
| 298 | `      return r.json().then(function(d) { throw new Error(d.error \|\| '更新失败'); });` |
| 304 | `    showMessage('操作成功', d.message \|\| '计划已更新');` |
| 308 | `    showMessage('更新失败', '更新失败: ' + err.message);` |
| 315 | `  showConfirm('确认删除', '确定要删除计划 "' + planName + '" 吗？删除后无法恢复！', function() {` |
| 348 | `    showMessage('操作成功', '计划创建成功');` |
| 352 | `    showMessage('创建失败', '创建失败: ' + err.message);` |

## admin-upstream-usage.html (21 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <p class="text-muted">上游 Key 按日聚合用量，用于与 Provider 对账</p>` |
| 10 | `      刷新` |
| 14 | `      导出 CSV` |
| 26 | `      <div class="stat-label">总请求数</div>` |
| 53 | `      <div class="stat-label">错误数</div>` |
| 62 | `    <h2 class="card-title">用量明细</h2>` |
| 65 | `        <option value="">全部 Provider</option>` |
| 70 | `      <input type="text" class="form-input" id="filter-model" placeholder="模型名称" style="width: 160px" onkeyup="if(event.key==='Enter')applyFilters()">` |
| 73 | `      <button class="btn btn-primary" onclick="applyFilters()">筛选</button>` |
| 74 | `      <button class="btn btn-outline" onclick="resetFilters()">重置</button>` |
| 79 | `    <span class="text-muted" id="record-count">共 {{.Data.Total}} 条记录</span>` |
| 120 | `  <p class="text-muted text-center py-8">暂无数据</p>` |
| 125 | `    <span class="pagination-info">共 {{.Data.Total}} 条，第 {{.Data.Page}} / {{.Data.TotalPages}} 页</span>` |
| 126 | `    <button class="btn btn-sm btn-secondary" disabled>上一页</button>` |
| 127 | `    <button class="btn btn-sm btn-secondary" onclick="fetchData(2)">下一页</button>` |
| 198 | `    document.getElementById('record-count').textContent = '共 ' + total + ' 条记录';` |
| 201 | `      tbody.innerHTML = '<tr><td colspan="11" class="text-center text-muted">暂无数据</td></tr>';` |
| 224 | `      pgHtml += '<span class="pagination-info">共 ' + total + ' 条，第 ' + currentPage + ' / ' + totalPages + ' 页</span>';` |
| 225 | `      pgHtml += '<button class="btn btn-sm btn-secondary" ' + (currentPage <= 1 ? 'disabled' : '') + ' onclick="fetchData(' + (currentPage - 1) + ')">` |
| 226 | `      pgHtml += '<button class="btn btn-sm btn-secondary" ' + (currentPage >= totalPages ? 'disabled' : '') + ' onclick="fetchData(' + (currentPage + ` |
| 230 | `    tbody.innerHTML = '<tr><td colspan="11" class="text-center text-danger">加载失败: ' + e.message + '</td></tr>';` |

## admin-users.html (17 处)

| 行号 | 代码 |
|------|------|
| 9 | `        <option value="">全部角色</option>` |
| 22 | `          <th>用户</th>` |
| 23 | `          <th>角色</th>` |
| 39 | `                <div class="font-medium">{{if .Name}}{{.Name}}{{else}}未设置{{end}}</div>` |
| 53 | `              <button class="btn btn-sm btn-danger" onclick="deleteUser('{{.ID}}')">删除</button>` |
| 62 | `  <p class="text-muted text-center mt-4">暂无用户</p>` |
| 66 | `    <span class="pagination-info">共 {{$d.Total}} 条，第 {{$d.Page}} / {{$d.TotalPages}} 页</span>` |
| 68 | `    <a class="btn btn-sm btn-secondary" href="/admin/users?page={{int (sub $d.Page 1)}}">上一页</a>` |
| 71 | `    <a class="btn btn-sm btn-secondary" href="/admin/users?page={{int (add $d.Page 1)}}">下一页</a>` |
| 81 | `      <span class="modal-title">用户详情</span>` |
| 92 | `  showConfirm('确认删除用户', '确定要删除该用户吗？用户数据会被标记为已删除，不再显示在列表中。此操作不可逆。', function() {` |
| 125 | `  document.getElementById('user-modal-body').innerHTML = '<div class="text-center py-4 text-muted">加载中...</div>';` |
| 134 | `      document.getElementById('user-modal-body').innerHTML = '<div class="text-center text-danger py-4">加载失败: ' + e.message + '</div>';` |
| 149 | `  if (btn) btn.textContent = input.disabled ? '修改余额' : '取消';` |
| 173 | `        '确定要将用户角色从 "' + userEditCurrentRole + '" 修改为 "' + payload.role + '" 吗？';` |
| 210 | `      showMessage('保存失败', data.error?.message \|\| '保存失败');` |
| 214 | `    showMessage('网络错误', '网络错误，请重试');` |

## admin.html (32 处)

| 行号 | 代码 |
|------|------|
| 6 | `    <p class="text-muted">管理后台概览</p>` |
| 12 | `        <span>用户管理</span>` |
| 16 | `        <span>用户组</span>` |
| 20 | `        <span>企业</span>` |
| 24 | `        <span>密钥池</span>` |
| 28 | `        <span>模型管理</span>` |
| 32 | `        <span>订阅</span>` |
| 36 | `        <span>上行监控</span>` |
| 40 | `        <span>下行监控</span>` |
| 52 | `      <div class="stat-label">总用户数</div>` |
| 57 | `        +{{$d.NewUsersThisWeek}} 本周` |
| 67 | `      <div class="stat-label">今日请求</div>` |
| 72 | `        {{if gt $d.RequestsTrend 0}}+{{end}}{{$d.RequestsTrend}}% 环比` |
| 82 | `      <div class="stat-label">活跃模型</div>` |
| 91 | `      <div class="stat-label">上游密钥</div>` |
| 94 | `      <div class="trend trend-down">{{$d.DisabledUpstreamKeys}} 已禁用</div>` |
| 107 | `      <div class="stat-label">活跃企业</div>` |
| 116 | `      <div class="stat-label">待处理告警</div>` |
| 125 | `      <div class="stat-label">平均延迟</div>` |
| 134 | `      <div class="stat-label">异常供应商</div>` |
| 143 | `    <h2 class="card-title">用量趋势</h2>` |
| 145 | `      <option value="7d">7天</option>` |
| 146 | `      <option value="14d">14天</option>` |
| 147 | `      <option value="30d">30天</option>` |
| 148 | `      <option value="90d">90天</option>` |
| 160 | `      <h2 class="card-title">最新注册用户</h2>` |
| 161 | `      <a href="/admin/users" class="card-link">查看全部</a>` |
| 166 | `          <tr><th>邮箱</th><th>角色</th><th>注册时间</th></tr>` |
| 185 | `      <h2 class="card-title">最近审计日志</h2>` |
| 186 | `      <a href="/admin/audit-logs" class="card-link">查看全部</a>` |
| 191 | `          <tr><th>用户</th><th>操作</th><th>时间</th></tr>` |
| 318 | `        label: '请求量',` |

## apikeys.html (109 处)

| 行号 | 代码 |
|------|------|
| 9 | `    <button class="btn btn-primary" onclick="openCreateModal()">+ 新建Key</button>` |
| 13 | `  <h2 style="padding: 16px 20px 8px; font-size: 16px; font-weight: 600; color: var(--text);">个人密钥</h2>` |
| 19 | `          <th>名称</th>` |
| 20 | `          <th>前缀</th>` |
| 21 | `          <th>类型</th>` |
| 23 | `          <th>路由模式</th>` |
| 24 | `          <th>模型</th>` |
| 25 | `          <th>状态</th>` |
| 26 | `          <th>操作</th>` |
| 35 | `            {{if eq .KeyType "subscription"}}套餐{{else if eq .KeyType "enterprise"}}企业{{else}}标准{{end}}` |
| 39 | `            {{if eq .ModelRoutingMode "single"}}单模型{{else if eq .ModelRoutingMode "round_robin"}}轮询{{else if eq .ModelRoutingMode "priority"}}优先级{{els` |
| 42 | `            {{if .CurrentModel}}{{.CurrentModel}}{{else if .SelectedModels}}{{len .SelectedModels}}个模型{{else}}—{{end}}` |
| 44 | `          <td><span class="badge badge-{{if eq .Status "active"}}success{{else}}error{{end}}">{{if eq .Status "active"}}活跃{{else}}禁用{{end}}</span></td` |
| 47 | `              <button class="btn btn-sm btn-secondary" onclick='openSettings({{.ID}})'>设置</button>` |
| 48 | `              <button class="btn btn-sm btn-ghost" onclick="copyKey('{{.ID}}')" title="复制Key">复制</button>` |
| 49 | `              <button class="btn btn-sm btn-danger" onclick='confirmDeleteKey({{.ID}}, "{{.Name}}")'>删除</button>` |
| 61 | `    <span class="pagination-info">共 {{$personalKeys.Total}} 条，第 {{$personalKeys.Page}} / {{$personalKeys.TotalPages}} 页</span>` |
| 63 | `    <a class="btn btn-sm btn-secondary" href="/apikeys?page={{int (sub $personalKeys.Page 1)}}">上一页</a>` |
| 66 | `    <a class="btn btn-sm btn-secondary" href="/apikeys?page={{int (add $personalKeys.Page 1)}}">下一页</a>` |
| 72 | `  <p class="text-muted text-center" style="padding: 16px 0;">暂无个人密钥</p>` |
| 77 | `  <h2 style="padding: 24px 20px 8px; font-size: 16px; font-weight: 600; color: var(--text); border-top: 1px solid var(--border);">企业分配密钥</h2>` |
| 83 | `          <th>名称</th>` |
| 84 | `          <th>前缀</th>` |
| 85 | `          <th>来源</th>` |
| 86 | `          <th>模型</th>` |
| 87 | `          <th>状态</th>` |
| 88 | `          <th>操作</th>` |
| 96 | `          <td class="text-sm">管理员分配</td>` |
| 102 | `            {{else}}全部模型 ({{.TotalModels}}){{end}}` |
| 104 | `          <td><span class="badge badge-{{if eq .Status "active"}}success{{else}}error{{end}}">{{if eq .Status "active"}}正常{{else}}已禁用{{end}}</span></t` |
| 106 | `            <button class="btn btn-sm btn-secondary" onclick="viewEnterpriseKey('{{.ID}}')">复制</button>` |
| 114 | `  <p class="text-muted text-center" style="padding: 16px 0;">暂无企业分配密钥</p>` |
| 123 | `      <span class="modal-title">新建 API Key</span>` |
| 131 | `          <label class="form-label">Key 类型 *</label>` |
| 136 | `                <div style="font-weight:600;">标准 Key</div>` |
| 138 | `                <span style="font-size:12px; color:var(--text-muted); margin-left:8px;">标准/折扣计费，多Group可选</span>` |
| 144 | `                <div style="font-weight:600;">套餐 Key</div>` |
| 146 | `                <span style="font-size:12px; color:var(--text-muted); margin-left:8px;">Token/Code Plan 预付费套餐</span>` |
| 152 | `                <div style="font-weight:600;">企业 Key</div>` |
| 154 | `                <span style="font-size:12px; color:var(--text-muted); margin-left:8px;">企业客户（需管理员授权）</span>` |
| 161 | `          <label class="form-label">名称 *</label>` |
| 162 | `          <input type="text" name="name" class="form-input" placeholder="例如: 生产环境" required>` |
| 165 | `          <label class="form-label">模型路由模式</label>` |
| 168 | `              <input type="radio" name="model_routing_mode" value="single" checked onchange="onRoutingModeChange()"> 单一模型` |
| 171 | `              <input type="radio" name="model_routing_mode" value="round_robin" onchange="onRoutingModeChange()"> 轮询负载均衡` |
| 174 | `              <input type="radio" name="model_routing_mode" value="priority" onchange="onRoutingModeChange()"> 优先级顺序` |
| 179 | `          <label class="form-label">选择 Group</label>` |
| 182 | `              <option value="">-- 选择 Group --</option>` |
| 185 | `              <option value="">-- 备用 Group (可选) --</option>` |
| 188 | `              <option value="">-- 备用 Group (可选) --</option>` |
| 193 | `          <label class="form-label">选择模型</label>` |
| 195 | `            <option value="">-- 先选择 Group --</option>` |
| 200 | `          <label class="form-label">选择多个模型</label>` |
| 203 | `            <label class="form-label">已选模型顺序 <span class="text-xs text-muted">（拖拽调整优先级）</span></label>` |
| 210 | `      <button class="btn btn-secondary" onclick="closeCreateModal()">取消</button>` |
| 211 | `      <button class="btn btn-primary" onclick="submitCreateKey()">确认创建</button>` |
| 220 | `      <span class="modal-title" id="settings-title">Key 设置</span>` |
| 224 | `      <div class="text-center text-muted py-8">加载中...</div>` |
| 233 | `      <span class="modal-title" id="confirm-title">确认</span>` |
| 240 | `      <button class="btn btn-secondary" onclick="closeConfirmModal()">取消</button>` |
| 241 | `      <button class="btn btn-danger" id="confirm-btn">确认</button>` |
| 250 | `      <span class="modal-title" id="message-title">提示</span>` |
| 257 | `      <button class="btn btn-primary" onclick="closeMessageModal()">确定</button>` |
| 266 | `      <span class="modal-title">Key 创建成功</span>` |
| 270 | `      <p class="text-danger mb-2"><strong>请立即复制保存，此信息仅显示一次！</strong></p>` |
| 276 | `      <button class="btn btn-primary" onclick="copyCreatedKey()">复制 Key</button>` |
| 277 | `      <button class="btn btn-secondary" onclick="closeKeyCreatedModal()">关闭</button>` |
| 367 | `    select.innerHTML = '<option value="">' + (i === 1 ? '-- 选择 Group --' : '-- 备用 Group (可选) --') + '</option>';` |
| 391 | `  singleSelect.innerHTML = '<option value="">-- 选择模型 --</option>';` |
| 398 | `    singleSelect.innerHTML = '<option value="">请先选择 Group</option>';` |
| 422 | `      var eolBadge = eol ? ' [EOL ' + eol.eolDaysRemaining + '天后停服]' : '';` |
| 443 | `        eolWarning.innerHTML = '<span style="color:#b45309;">此模型将于 ' + eol.eolServiceAt + ' 停止服务（剩余 ' + eol.eolDaysRemaining + ' 天），建议选择其他模型</span>';` |
| 568 | `      showMessage('提示', '已复制到剪贴板');` |
| 577 | `    try { document.execCommand('copy'); showMessage('提示', '已复制到剪贴板'); }` |
| 578 | `    catch(e) { showMessage('错误', '复制失败，请手动复制'); }` |
| 587 | `  if (!nameInput.value.trim()) { showMessage('提示', '请输入 Key 名称'); return; }` |
| 599 | `    if (data.selected_models.length === 0) { showMessage('提示', '请至少选择一个模型'); return; }` |
| 602 | `    if (!data.current_model) { showMessage('提示', '请选择一个模型'); return; }` |
| 610 | `  if (data.group_priority.length === 0) { showMessage('提示', '请至少选择一个 Group'); return; }` |
| 623 | `      showMessage('创建失败', (res.error && res.error.message) \|\| '请重试');` |
| 626 | `  .catch(function() { showMessage('错误', '请求失败，请重试'); });` |
| 632 | `  document.getElementById('settings-body').innerHTML = '<div class="text-center text-muted py-8">加载中...</div>';` |
| 633 | `  document.getElementById('settings-title').textContent = 'Key 设置';` |
| 642 | `      document.getElementById('settings-body').innerHTML = '<p class="text-danger">加载失败</p>';` |
| 647 | `  var routingOpts = '<option value="single"' + (key.model_routing_mode === 'single' ? ' selected' : '') + '>单一模型</option>' +` |
| 648 | `    '<option value="round_robin"' + (key.model_routing_mode === 'round_robin' ? ' selected' : '') + '>轮询负载均衡</option>' +` |
| 649 | `    '<option value="priority"' + (key.model_routing_mode === 'priority' ? ' selected' : '') + '>优先级顺序</option>';` |
| 671 | `    '<div class="form-group"><label class="form-label">名称</label>' +` |
| 674 | `    '<div class="form-group"><label class="form-label">路由模式</label>' +` |
| 677 | `    '<div id="settings-single-section" style="display:' + showSingle + '"><div class="form-group"><label class="form-label">当前模型</label>' +` |
| 679 | `    '<option value="">-- 选择模型 --</option>' +` |
| 683 | `    '<div id="settings-multi-section" style="display:' + showMulti + '"><div class="form-group"><label class="form-label">选择多个模型</label>' +` |
| 686 | `    '<label class="form-label">已选模型顺序 <span class="text-xs text-muted">（拖拽调整优先级）</span></label>' +` |
| 691 | `    '<button type="button" class="btn btn-secondary" onclick="closeSettingsModal()">取消</button>' +` |
| 692 | `    '<button type="button" class="btn btn-primary" onclick="saveSettings(\'' + keyId + '\')">保存</button>' +` |
| 803 | `      showMessage('提示', '请至少选择一个模型');` |
| 820 | `      showMessage('保存失败', (res.error && res.error.message) \|\| '请重试');` |
| 823 | `  .catch(function() { showMessage('错误', '请求失败'); });` |
| 828 | `  showConfirm('删除 Key', '确定要删除 "' + keyName + '" 吗？此操作不可恢复。', function() {` |
| 833 | `        else { showMessage('删除失败', (res.error && res.error.message) \|\| '请重试'); }` |
| 835 | `      .catch(function() { showMessage('错误', '请求失败'); });` |
| 845 | `        showMessage('错误', res.error.message \|\| '获取密钥失败');` |
| 849 | `        document.getElementById('key-created-modal').querySelector('.modal-header .modal-title').textContent = '企业密钥';` |
| 850 | `        document.getElementById('key-created-modal').querySelector('.modal-body p').textContent = '请妥善保存密钥，关闭后无法再次查看。';` |
| 854 | `    .catch(function() { showMessage('错误', '请求失败'); });` |
| 865 | `        document.getElementById('key-created-modal').querySelector('.modal-header .modal-title').textContent = '个人密钥';` |
| 866 | `        document.getElementById('key-created-modal').querySelector('.modal-body p').textContent = '请妥善保存密钥，关闭后无法再次查看。';` |
| 870 | `        showMessage('提示', '无法获取完整密钥');` |
| 873 | `    .catch(function() { showMessage('错误', '请求失败'); });` |

## billing-details.html (60 处)

| 行号 | 代码 |
|------|------|
| 3 | `  <h1 class="page-title">计费明细</h1>` |
| 7 | `    <button class="tab-btn active" onclick="switchTab('realtime')">实时明细</button>` |
| 8 | `    <button class="tab-btn" onclick="switchTab('monthly')">月账单</button>` |
| 9 | `    <button class="tab-btn" onclick="switchTab('requests')">请求明细</button>` |
| 15 | `    <!-- 实时明细 Tab -->` |
| 19 | `          <h2 class="card-title">实时消费明细</h2>` |
| 23 | `              导出` |
| 31 | `                <th>产品名称</th>` |
| 32 | `                <th>产品类型</th>` |
| 33 | `                <th>消费（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 34 | `                <th>兑换码扣减（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 35 | `                <th>充值扣减（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 36 | `                <th>Credit扣减（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 37 | `                <th>订阅扣减（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 42 | `                <td colspan="7" class="text-center text-muted py-8">加载中...</td>` |
| 50 | `    <!-- 月账单 Tab -->` |
| 54 | `          <h2 class="card-title">月账单</h2>` |
| 58 | `              导出` |
| 66 | `            <label class="filter-label">日期范围</label>` |
| 69 | `              <span class="filter-separator">至</span>` |
| 74 | `            <button class="btn btn-primary btn-sm" onclick="loadMonthlyData()">筛选</button>` |
| 75 | `            <button class="btn btn-secondary btn-sm" onclick="resetMonthlyFilters()">重置</button>` |
| 83 | `                <th>日期</th>` |
| 84 | `                <th>消费（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 85 | `                <th>兑换码扣减（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 86 | `                <th>充值扣减（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 87 | `                <th>Credit扣减（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 88 | `                <th>订阅扣减（{{if eq .Currency "USD"}}$ {{else}}￥ {{end}}）</th>` |
| 93 | `                <td colspan="6" class="text-center text-muted py-8">加载中...</td>` |
| 98 | `                <td><strong>合计</strong></td>` |
| 111 | `    <!-- 请求明细 Tab -->` |
| 115 | `          <h2 class="card-title">请求明细</h2>` |
| 119 | `              导出` |
| 127 | `            <label class="filter-label">日期范围</label>` |
| 130 | `              <span class="filter-separator">至</span>` |
| 135 | `            <button class="btn btn-primary btn-sm" onclick="loadRequestsData()">筛选</button>` |
| 136 | `            <button class="btn btn-secondary btn-sm" onclick="resetRequestsFilters()">重置</button>` |
| 144 | `                <th>请求 ID</th>` |
| 145 | `                <th>模型</th>` |
| 146 | `                <th>时间</th>` |
| 147 | `                <th>项目名称</th>` |
| 149 | `                <th>输入 Tokens</th>` |
| 150 | `                <th>输出 Tokens</th>` |
| 156 | `                <td colspan="8" class="text-center text-muted py-8">加载中...</td>` |
| 164 | `          <button class="btn btn-sm btn-secondary" id="req-prev-btn" onclick="changeRequestsPage(-1)" disabled>上一页</button>` |
| 165 | `          <span class="pagination-info" id="requests-page-info">第 1 页</span>` |
| 166 | `          <button class="btn btn-sm btn-secondary" id="req-next-btn" onclick="changeRequestsPage(1)">下一页</button>` |
| 338 | `// 实时明细` |
| 341 | `  tbody.innerHTML = '<tr><td colspan="7" class="text-center text-muted py-8">加载中...</td></tr>';` |
| 349 | `      tbody.innerHTML = '<tr><td colspan="7" class="text-center text-muted py-8">暂无数据</td></tr>';` |
| 365 | `    tbody.innerHTML = '<tr><td colspan="7" class="text-center text-muted py-8">加载失败</td></tr>';` |
| 369 | `// 月账单` |
| 374 | `  tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted py-8">加载中...</td></tr>';` |
| 387 | `      tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted py-8">暂无数据</td></tr>';` |
| 417 | `    tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted py-8">加载失败</td></tr>';` |
| 427 | `// 请求明细` |
| 433 | `  tbody.innerHTML = '<tr><td colspan="8" class="text-center text-muted py-8">加载中...</td></tr>';` |
| 449 | `      tbody.innerHTML = '<tr><td colspan="8" class="text-center text-muted py-8">暂无数据</td></tr>';` |
| 468 | `    document.getElementById('requests-page-info').textContent = '第 ' + requestsPage + ' 页 / 共 ' + requestsTotalPages + ' 页';` |
| 472 | `    tbody.innerHTML = '<tr><td colspan="8" class="text-center text-muted py-8">加载失败</td></tr>';` |

## billing.html (69 处)

| 行号 | 代码 |
|------|------|
| 3 | `  <h1 class="page-title">账户充值</h1>` |
| 9 | `        <li>请确保您的账户有足够金额进行交易。</li>` |
| 10 | `        <li>若充值过程遇到交易问题，请前往相应的第三方支付平台进行确认。</li>` |
| 11 | `        <li>为了保障您的账户安全与充值体验的顺畅，您每月最多可以享受30次充值服务。1天内未支付的订单，会自动关闭。</li>` |
| 12 | `        <li>充值完成后可前往 <a href="/dashboard" class="text-primary">账户总览</a> 查看账户余额。</li>` |
| 19 | `    <h2 class="card-title">充值汇款</h2>` |
| 26 | `        <label class="form-label">支付金额 <span class="text-xs text-muted">({{if eq .Currency "USD"}}USD{{else}}CNY{{end}})</span>：</label>` |
| 28 | `          <button type="button" class="amount-btn" data-amount="50" onclick="selectAmount(this, 50)">{{if eq .Currency "USD"}}$50{{else}}50元{{end}}</b` |
| 29 | `          <button type="button" class="amount-btn" data-amount="100" onclick="selectAmount(this, 100)">{{if eq .Currency "USD"}}$100{{else}}100元{{end}` |
| 30 | `          <button type="button" class="amount-btn active" data-amount="200" onclick="selectAmount(this, 200)">{{if eq .Currency "USD"}}$200{{else}}200` |
| 31 | `          <button type="button" class="amount-btn" data-amount="500" onclick="selectAmount(this, 500)">{{if eq .Currency "USD"}}$500{{else}}500元{{end}` |
| 32 | `          <button type="button" class="amount-btn" data-amount="1000" onclick="selectAmount(this, 1000)">{{if eq .Currency "USD"}}$1000{{else}}1000元{{` |
| 33 | `          <button type="button" class="amount-btn" data-amount="2000" onclick="selectAmount(this, 2000)">{{if eq .Currency "USD"}}$2000{{else}}2000元{{` |
| 34 | `          <button type="button" class="amount-btn" data-amount="5000" onclick="selectAmount(this, 5000)">{{if eq .Currency "USD"}}$5000{{else}}5000元{{` |
| 35 | `          <button type="button" class="amount-btn amount-btn-custom" onclick="toggleCustomAmount()">自定义</button>` |
| 38 | `          <input type="number" id="custom-amount" name="custom_amount" class="form-input" placeholder="请输入自定义金额" min="1" max="100000" oninput="updateC` |
| 39 | `          <span class="input-suffix">{{if eq .Currency "USD"}}USD{{else}}元{{end}}</span>` |
| 46 | `        <label class="form-label">支付方式：</label>` |
| 50 | `            <span>微信支付</span>` |
| 54 | `            <span>支付宝</span>` |
| 71 | `          <span>已阅读同意《<a href="#" class="text-primary" onclick="showPaymentTermsModal(); return false;">充值协议</a>》</span>` |
| 78 | `          确认充值` |
| 89 | `      <span class="modal-title">订单摘要</span>` |
| 95 | `          <span class="order-label">订单号</span>` |
| 99 | `          <span class="order-label">金额</span>` |
| 103 | `          <span class="order-label">状态</span>` |
| 104 | `          <span class="order-value"><span class="badge badge-warning">待支付</span></span>` |
| 107 | `          <span class="order-label">支付方式</span>` |
| 113 | `      <button class="btn btn-secondary" onclick="closeOrderModal()">取消</button>` |
| 114 | `      <button class="btn btn-secondary" onclick="payLater()">稍后支付</button>` |
| 115 | `      <button class="btn btn-primary" id="confirm-pay-btn" onclick="confirmPay()">确认支付</button>` |
| 308 | `    'wechat': '微信支付',` |
| 309 | `    'alipay': '支付宝',` |
| 327 | `    showToast('error', '请选择或输入充值金额');` |
| 333 | `    showToast('error', '请阅读并同意充值协议');` |
| 339 | `  btn.textContent = '处理中...';` |
| 357 | `      showToast('error', data.error.message \|\| '创建订单失败');` |
| 360 | `    btn.textContent = '确认充值';` |
| 363 | `    showToast('error', '请求失败，请重试');` |
| 365 | `    btn.textContent = '确认充值';` |
| 388 | `  showToast('success', '订单已保存，您可稍后在充值明细中完成支付');` |
| 396 | `  btn.textContent = '处理中...';` |
| 412 | `      showToast('success', '支付成功！');` |
| 417 | `      showToast('error', data.error.message \|\| '支付失败');` |
| 419 | `      btn.textContent = '确认支付';` |
| 423 | `    showToast('error', '请求失败，请重试');` |
| 425 | `    btn.textContent = '确认支付';` |
| 434 | `      <span class="modal-title">充值协议</span>` |
| 438 | `      <p style="background:var(--primary-light);padding:12px;border-radius:8px;margin-bottom:16px;color:var(--text)">WOODGIT 充值与支付条款 &mdash; 最后更新：2026` |
| 440 | `      <h3 style="color:var(--text);margin-bottom:8px">1. 充值性质</h3>` |
| 441 | `      <p style="margin-bottom:16px">本平台采用<strong>预充值消费模式</strong>，您需要预先充值到账户余额后方可使用 API 服务。充值金额将计入您的账户余额，用于抵扣 API 调用产生的费用。账户余额为虚拟货币单位（人民币），仅可用于本平台 API` |
| 443 | `      <h3 style="color:var(--text);margin-bottom:8px">2. 计费方式</h3>` |
| 444 | `      <p style="margin-bottom:16px">本平台按 Token 用量计费，根据不同模型和请求类型（输入/输出）分别计价。每次请求的 Token 用量以上游服务提供商返回的用量数据为准。费用 = 输入 Token 数 × 输入单价 + 输出 Token 数 × 输出单价。` |
| 446 | `      <h3 style="color:var(--text);margin-bottom:8px">3. 余额管理</h3>` |
| 447 | `      <p style="margin-bottom:16px">充值余额<strong>无有效期限制</strong>，不会过期失效。但如账户连续 12 个月未有任何登录或使用记录，我们有权冻结该账户。账户余额不可转让给其他用户账户。</p>` |
| 449 | `      <h3 style="color:var(--text);margin-bottom:8px">4. 退款政策</h3>` |
| 450 | `      <p style="margin-bottom:8px"><strong style="color:var(--warning)">充值余额一般情况下不支持退款。</strong>以下情形可申请退款：</p>` |
| 452 | `        <li>因我们原因终止服务，退还扣除已消费金额后的剩余余额</li>` |
| 453 | `        <li>因法律政策变化、上游服务终止等不可抗力导致服务无法继续</li>` |
| 454 | `        <li>我们基于自身经营需要或风险评估决定终止向您提供服务</li>` |
| 455 | `        <li>法律法规有强制性退款规定的</li>` |
| 457 | `      <p style="margin-bottom:16px">退款金额 = 充值余额 - 已消费金额 - 渠道手续费。退款将在审核通过后 7-15 个工作日内原路退回。</p>` |
| 459 | `      <h3 style="color:var(--text);margin-bottom:8px">5. 兑换码</h3>` |
| 460 | `      <p style="margin-bottom:16px">兑换码可在充值页面输入并兑换为账户余额。每个兑换码仅可使用一次，使用后即失效。兑换码不可转让、不可兑换现金、不可退款。严禁买卖兑换码，违者账户将被冻结。</p>` |
| 462 | `      <h3 style="color:var(--text);margin-bottom:8px">6. 异常处理</h3>` |
| 463 | `      <p style="margin-bottom:16px">如充值后余额未到账，请保留支付凭证并联系客服，我们将在 1 个工作日内处理。如对计费有异议，请在消费后 7 天内提出申诉。因系统故障导致重复扣费的，我们将在核实后自动退还多扣金额。</p>` |
| 465 | `      <h3 style="color:var(--text);margin-bottom:8px">7. 合规声明</h3>` |
| 466 | `      <p>本平台充值与支付服务遵守中华人民共和国相关法律法规。我们不会以任何形式挪用用户充值资金。所有交易记录保留 7 年以备审计。</p>` |
| 469 | `      <button class="btn btn-primary" onclick="closePaymentTermsModal()">我已了解</button>` |

## dashboard.html (19 处)

| 行号 | 代码 |
|------|------|
| 16 | `        <span>充值</span>` |
| 20 | `        <span>订阅</span>` |
| 24 | `        <span>计费</span>` |
| 86 | `      <div class="stat-label">活跃订阅</div>` |
| 95 | `      <div class="stat-label">Token 套餐剩余</div>` |
| 104 | `      <div class="stat-label">Request 套餐剩余</div>` |
| 113 | `    <h2 class="card-title">最近使用的 Keys</h2>` |
| 114 | `    <a href="/apikeys" class="btn btn-sm btn-secondary">查看全部</a>` |
| 121 | `    <div id="recent-keys-loading" class="text-center py-4 text-muted htmx-indicator">加载中...</div>` |
| 130 | `    <h2 class="card-title">发票申请</h2>` |
| 131 | `    <a href="/invoices" class="btn btn-sm btn-secondary">查看全部</a>` |
| 133 | `  <p class="text-sm text-muted mt-2">您有 {{$d.PendingInvoices}} 个待处理的发票申请</p>` |
| 139 | `          <th>抬头</th>` |
| 140 | `          <th>金额</th>` |
| 141 | `          <th>状态</th>` |
| 142 | `          <th>时间</th>` |
| 151 | `            {{if eq .Status "pending"}}<span class="badge badge-warning">待处理</span>` |
| 152 | `            {{else if eq .Status "completed"}}<span class="badge badge-success">已开具</span>` |
| 153 | `            {{else if eq .Status "rejected"}}<span class="badge badge-error">已拒绝</span>` |

## enterprise-accept.html (18 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="auth-title">接受企业邀请</h1>` |
| 8 | `    <a href="/login" class="btn btn-primary" style="width:100%; margin-top:16px;">返回登录</a>` |
| 11 | `      您已被邀请加入企业 <strong>{{.EnterpriseName}}</strong>` |
| 18 | `        <label class="form-label">邮箱</label>` |
| 23 | `        <label class="form-label">姓名 *</label>` |
| 24 | `        <input type="text" name="name" class="form-input" required placeholder="您的姓名">` |
| 28 | `        <label class="form-label">设置密码 *</label>` |
| 29 | `        <input type="password" name="password" id="accept-password" class="form-input" required placeholder="至少8位" minlength="8">` |
| 34 | `        <label class="form-label">确认密码 *</label>` |
| 35 | `        <input type="password" name="confirm_password" id="accept-confirm-password" class="form-input" required placeholder="再次输入密码" minlength="8">` |
| 39 | `      <p class="text-xs text-muted mb-4">密码需至少8位，包含大小写字母、数字和特殊字符</p>` |
| 41 | `      <button type="submit" class="btn btn-primary" style="width:100%;" id="accept-btn">接受邀请并注册</button>` |
| 45 | `      已有账号？<a href="/login">直接登录</a>` |
| 110 | `    if (s <= 2) { strengthEl.textContent = '密码强度：弱'; strengthEl.className = 'text-xs mt-1 strength-weak'; }` |
| 111 | `    else if (s <= 3) { strengthEl.textContent = '密码强度：中'; strengthEl.className = 'text-xs mt-1 strength-medium'; }` |
| 112 | `    else { strengthEl.textContent = '密码强度：强'; strengthEl.className = 'text-xs mt-1 strength-strong'; }` |
| 121 | `    if (p1 === p2) { matchEl.textContent = '密码一致'; matchEl.className = 'text-xs mt-1 match-ok'; btn.disabled = false; }` |
| 122 | `    else { matchEl.textContent = '密码不一致'; matchEl.className = 'text-xs mt-1 match-fail'; btn.disabled = true; }` |

## forgot-password.html (1 处)

| 行号 | 代码 |
|------|------|
| 15 | `      <button type="submit" class="btn btn-primary w-full">发送重置链接</button>` |

## invitation.html (41 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">邀请码</h1>` |
| 5 | `    <button class="btn btn-primary" id="btn-generate" onclick="generateCode()">生成邀请码</button>` |
| 11 | `      <div class="stat-label">邀请码</div>` |
| 16 | `      <div class="stat-label">邀请奖励余额</div>` |
| 20 | `      <div class="stat-label">累计邀请奖励</div>` |
| 28 | `        <tr><th>邀请码</th><th>邀请链接</th><th>已用/上限</th><th>邀请奖励</th><th>注册奖励</th><th>有效期</th><th>状态</th><th>创建时间</th></tr>` |
| 39 | `      <span class="modal-title">邀请码生成成功</span>` |
| 44 | `      <button class="btn btn-primary" onclick="closeGenerateResultModal()">确定</button>` |
| 52 | `    <h2 class="card-title">奖励转余额</h2>` |
| 54 | `  <p class="text-sm text-muted mb-4">将邀请奖励余额转入账户余额，可用于 API 调用消费。转入后按"赠送余额→充值余额→普通余额"顺序消费。</p>` |
| 56 | `    <input type="number" id="transfer-amount" class="form-input" placeholder="转入金额" step="0.01" min="0.01" style="width:200px">` |
| 57 | `    <button class="btn btn-primary" onclick="transferQuota()">转入</button>` |
| 64 | `    <h2 class="card-title">邀请记录</h2>` |
| 69 | `        <tr><th>邀请码</th><th>被邀请人</th><th>邀请人奖励</th><th>注册奖励</th><th>时间</th></tr>` |
| 76 | `    <button class="btn btn-sm btn-secondary" id="records-prev-btn" onclick="recordsPage--;loadRecords()" disabled>上一页</button>` |
| 77 | `    <button class="btn btn-sm btn-secondary" id="records-next-btn" onclick="recordsPage++;loadRecords()">下一页</button>` |
| 138 | `      showMessage('成功', '邀请链接已复制到剪贴板');` |
| 156 | `    showMessage('成功', '邀请链接已复制到剪贴板');` |
| 158 | `    showMessage('复制失败', '您的浏览器不支持自动复制，请手动复制: ' + text);` |
| 173 | `      document.getElementById('stat-codes-limit').textContent = '上限 ' + data.max_codes + ' 个';` |
| 176 | `        btn.textContent = '已达上限';` |
| 179 | `        btn.textContent = '生成邀请码';` |
| 194 | `      var statusBadge = c.status==='active'?'<span class="badge badge-success">活跃</span>':` |
| 195 | `                        '<span class="badge badge-warning">禁用</span>';` |
| 196 | `      var expiresAt = c.expires_at ? formatDate(c.expires_at) : '永久';` |
| 199 | `        statusBadge = '<span class="badge badge-warning">已过期</span>';` |
| 202 | `        '<button class="btn btn-sm btn-secondary" onclick="copyInviteLink(\''+c.code+'\')" title="复制邀请链接">复制链接</button>' : '-';` |
| 203 | `      list.innerHTML += '<tr><td><span>'+c.code+'</span></td><td>'+inviteLink+'</td><td>'+c.use_count+'/'+(c.max_uses\|\|'无限')+'</td><td>'+c.reward_am` |
| 216 | `      list.innerHTML = '<tr><td colspan="5" style="text-align:center;color:var(--text-muted);padding:24px">暂无邀请记录</td></tr>';` |
| 220 | `      var inviterReward = r.reward_granted?'<span class="badge badge-success">已发放</span>':'<span class="badge badge-warning">未发放</span>';` |
| 221 | `      var inviteeReward = r.invitee_reward_granted?'<span class="badge badge-success">已发放</span>':'<span class="badge badge-warning">未发放</span>';` |
| 229 | `      document.getElementById('records-page-info').textContent = '共 ' + data.total + ' 条，第 ' + recordsPage + ' / ' + recordsTotalPages + ' 页';` |
| 244 | `      showMessage('错误', (data.error && data.error.message) ? data.error.message : '生成失败');` |
| 251 | `  var html = '<div class="form-group"><label class="form-label">邀请码</label><div class="flex gap-2"><span class="text-lg">' + data.code + '</span><butt` |
| 252 | `    '<div class="form-group mt-3"><label class="form-label">邀请链接</label><div class="flex gap-2"><input type="text" class="form-input" value="' + link ` |
| 253 | `    '<div class="mt-3 text-sm text-muted">邀请奖励: ' + data.reward_amount + ' \| 注册奖励: ' + data.invitee_reward_amount + '</div>';` |
| 268 | `  try { document.execCommand('copy'); showCopyOK(btn); } catch(e) { showMessage('复制失败', '请手动复制'); }` |
| 273 | `  if (btn) { var orig = btn.textContent; btn.textContent = '已复制'; setTimeout(function() { btn.textContent = orig; }, 1200); }` |
| 283 | `    showMessage('错误', '请输入有效金额');` |
| 288 | `      showMessage('成功', '已转入 ' + data.amount);` |
| 292 | `      showMessage('错误', (data.error && data.error.message) ? data.error.message : '转入失败');` |

## invoices.html (121 处)

| 行号 | 代码 |
|------|------|
| 8 | `      <button class="btn btn-secondary btn-sm" onclick="showRequestModal()">申请发票</button>` |
| 9 | `      <button class="btn btn-primary btn-sm" onclick="showAddProfileModal()">添加抬头</button>` |
| 17 | `    <h2 class="card-title">发票抬头</h2>` |
| 20 | `    <p class="text-muted text-center py-4">加载中...</p>` |
| 27 | `    <h2 class="card-title">申请记录</h2>` |
| 31 | `    <span>发票申请提交后由管理员人工审核开具，处理时间约 1-3 个工作日</span>` |
| 34 | `    <p class="text-muted text-center py-4">加载中...</p>` |
| 38 | `    <span class="pagination-info">共 {{$d.Total}} 条，第 {{$d.Page}} / {{$d.TotalPages}} 页</span>` |
| 40 | `    <a class="btn btn-sm btn-secondary" href="/invoices?page={{int (sub $d.Page 1)}}">上一页</a>` |
| 43 | `    <a class="btn btn-sm btn-secondary" href="/invoices?page={{int (add $d.Page 1)}}">下一页</a>` |
| 53 | `      <span class="modal-title" id="profile-modal-title">添加发票抬头</span>` |
| 60 | `        <label class="form-label">发票类型 *</label>` |
| 62 | `          <option value="personal">个人发票</option>` |
| 63 | `          <option value="normal">增值税普通发票（企业）</option>` |
| 64 | `          <option value="special">增值税专用发票（企业）</option>` |
| 65 | `          <option value="proforma">形式发票（国际）</option>` |
| 70 | `        <label class="form-label" id="inv-name-label">抬头名称 *</label>` |
| 71 | `        <input type="text" id="inv-name" class="form-input" placeholder="个人姓名或公司全称">` |
| 77 | `          <label class="form-label">身份证号（选填）</label>` |
| 78 | `          <input type="text" id="inv-id-card" class="form-input" placeholder="用于报销">` |
| 85 | `          <label class="form-label">纳税人识别号 *</label>` |
| 86 | `          <input type="text" id="inv-tax-number" class="form-input" placeholder="统一社会信用代码">` |
| 89 | `          <label class="form-label">公司地址</label>` |
| 90 | `          <input type="text" id="inv-registered-address" class="form-input" placeholder="注册地址">` |
| 94 | `            <label class="form-label">电话</label>` |
| 95 | `            <input type="text" id="inv-phone" class="form-input" placeholder="公司电话">` |
| 98 | `            <label class="form-label">邮箱</label>` |
| 99 | `            <input type="email" id="inv-email" class="form-input" placeholder="接收电子发票">` |
| 104 | `      <!-- Special (VAT 专用) -->` |
| 107 | `          <label class="form-label">开户银行 *</label>` |
| 108 | `          <input type="text" id="inv-bank-name" class="form-input" placeholder="中国工商银行XX支行">` |
| 111 | `          <label class="form-label">银行账号 *</label>` |
| 112 | `          <input type="text" id="inv-bank-account" class="form-input" placeholder="银行账号">` |
| 119 | `          <label class="form-label">国家/地区 *</label>` |
| 121 | `            <option value="US">美国</option>` |
| 122 | `            <option value="GB">英国</option>` |
| 123 | `            <option value="DE">德国</option>` |
| 124 | `            <option value="FR">法国</option>` |
| 125 | `            <option value="JP">日本</option>` |
| 126 | `            <option value="KR">韩国</option>` |
| 127 | `            <option value="SG">新加坡</option>` |
| 128 | `            <option value="AU">澳大利亚</option>` |
| 129 | `            <option value="OTHER">其他</option>` |
| 153 | `          <span class="form-label mb-0">设为默认抬头</span>` |
| 159 | `      <button class="btn btn-secondary" onclick="document.getElementById('profile-modal').style.display='none'">取消</button>` |
| 160 | `      <button class="btn btn-primary" onclick="saveProfile()">保存</button>` |
| 169 | `      <span class="modal-title">申请发票</span>` |
| 174 | `        <label class="form-label">发票抬头 *</label>` |
| 176 | `          <option value="">请选择发票抬头</option>` |
| 181 | `        <label class="form-label">发票金额 *</label>` |
| 186 | `        <p class="text-xs text-muted mt-1">输入金额（{{if eq .Currency "USD"}}USD{{else}}元{{end}}），如 1234.40</p>` |
| 190 | `        <label class="form-label">发票内容</label>` |
| 191 | `        <input type="text" id="req-content" class="form-input" value="信息技术服务费" placeholder="发票内容描述">` |
| 196 | `        <label class="form-label">配送方式 *</label>` |
| 200 | `            <span>电子发票（邮件发送）</span>` |
| 204 | `            <span>纸质发票（邮寄）</span>` |
| 212 | `          <label class="form-label">接收邮箱</label>` |
| 214 | `          <p class="text-xs text-muted mt-1">电子发票将在开具后发送至此邮箱</p>` |
| 221 | `          <label class="form-label">寄送地址 *</label>` |
| 223 | `            <option value="">请选择地址</option>` |
| 225 | `          <p class="text-xs text-muted mt-1">请先在 <a href="/addresses" class="text-primary">地址管理</a> 中添加收货地址，再选择寄送地址</p>` |
| 232 | `      <button class="btn btn-secondary" onclick="document.getElementById('request-modal').style.display='none'">取消</button>` |
| 233 | `      <button class="btn btn-primary" onclick="submitRequest()">提交申请</button>` |
| 242 | `      <span class="modal-title text-error">确认删除</span>` |
| 246 | `      <p class="text-sm">确定要删除此发票抬头吗？</p>` |
| 249 | `      <button class="btn btn-secondary" onclick="document.getElementById('delete-profile-modal').style.display='none'">取消</button>` |
| 250 | `      <button class="btn btn-danger" onclick="executeDeleteProfile()">删除</button>` |
| 259 | `      <span class="modal-title" id="msg-title">提示</span>` |
| 266 | `      <button class="btn btn-primary" onclick="document.getElementById('msg-modal').style.display='none'">确定</button>` |
| 300 | `  'personal': '个人发票',` |
| 301 | `  'normal': '增值税普通发票',` |
| 302 | `  'special': '增值税专用发票',` |
| 303 | `  'proforma': '形式发票'` |
| 306 | `  'pending': '待处理',` |
| 307 | `  'processing': '处理中',` |
| 308 | `  'completed': '已开具',` |
| 309 | `  'rejected': '已拒绝'` |
| 312 | `  'CN':'中国','HK':'中国香港','TW':'中国台湾','JP':'日本','KR':'韩国',` |
| 313 | `  'SG':'新加坡','MY':'马来西亚','US':'美国','CA':'加拿大','GB':'英国',` |
| 314 | `  'DE':'德国','FR':'法国','AU':'澳大利亚','NZ':'新西兰','OTHER':'其他'` |
| 335 | `    container.innerHTML = '<p class="text-muted text-center py-4">暂无发票抬头，点击右上角添加</p>';` |
| 346 | `    if (p.is_default) html += '        <span class="badge badge-primary">默认</span>';` |
| 349 | `    if (p.tax_number) html += '      <div class="text-xs text-muted">税号: ' + p.tax_number + '</div>';` |
| 350 | `    if (p.company_name) html += '      <div class="text-xs text-muted">公司: ' + p.company_name + '</div>';` |
| 351 | `    if (p.email) html += '      <div class="text-xs text-muted">邮箱: ' + p.email + '</div>';` |
| 352 | `    if (p.country && p.country !== 'CN') html += '      <div class="text-xs text-muted">国家: ' + (countryNames[p.country] \|\| p.country) + '</div>';` |
| 355 | `    html += '      <button class="btn btn-ghost btn-sm" onclick="editProfile(\'' + p.id + '\')">编辑</button>';` |
| 356 | `    if (!p.is_default) html += '      <button class="btn btn-ghost btn-sm" onclick="setDefaultProfile(\'' + p.id + '\')">设为默认</button>';` |
| 357 | `    html += '      <button class="btn btn-ghost btn-sm text-error" onclick="confirmDeleteProfile(\'' + p.id + '\')">删除</button>';` |
| 368 | `  select.innerHTML = '<option value="">请选择发票抬头</option>';` |
| 381 | `  select.innerHTML = '<option value="">请选择寄送地址</option>';` |
| 396 | `    container.innerHTML = '<p class="text-muted text-center py-4">暂无申请记录</p>';` |
| 399 | `  var html = '<table class="data-table"><thead><tr><th>抬头</th><th>内容</th><th>金额</th><th>配送</th><th>状态</th><th>时间</th></tr></thead><tbody>';` |
| 403 | `    var delivery = r.delivery_method === 'paper' ? '纸质邮寄' : '电子发票';` |
| 420 | `  document.getElementById('profile-modal-title').textContent = '添加发票抬头';` |
| 444 | `  document.getElementById('profile-modal-title').textContent = '编辑发票抬头';` |
| 471 | `  if (type === 'personal') nameLabel.textContent = '姓名 *';` |
| 472 | `  else if (type === 'proforma') nameLabel.textContent = '抬头名称 *';` |
| 473 | `  else nameLabel.textContent = '公司全称 *';` |
| 486 | `    document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">请填写抬头名称</div>';` |
| 498 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">请填写纳税人识别号</div>';` |
| 502 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">纳税人识别号应为15-20位</div>';` |
| 506 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">邮箱格式不正确</div>';` |
| 510 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">电话格式不正确</div>';` |
| 518 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">请填写开户银行</div>';` |
| 522 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">请填写银行账号</div>';` |
| 533 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">请填写公司名称</div>';` |
| 537 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">请填写邮箱</div>';` |
| 541 | `      document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">邮箱格式不正确</div>';` |
| 553 | `    showMsg('保存成功', '发票抬头已保存');` |
| 556 | `    document.getElementById('profile-result').innerHTML = '<div class="flash flash-error text-sm">' + (r.error?.message \|\| '保存失败') + '</div>';` |
| 572 | `    showMsg('删除成功', '发票抬头已删除');` |
| 575 | `    showMsg('删除失败', r.error?.message \|\| '删除失败');` |
| 591 | `  document.getElementById('req-content').value = '信息技术服务费';` |
| 622 | `    document.getElementById('request-result').innerHTML = '<div class="flash flash-error text-sm">请选择发票抬头</div>';` |
| 626 | `    document.getElementById('request-result').innerHTML = '<div class="flash flash-error text-sm">请填写有效金额（{{if eq .Currency "USD"}}USD{{else}}元{{end}}` |
| 640 | `      document.getElementById('request-result').innerHTML = '<div class="flash flash-error text-sm">请填写接收邮箱</div>';` |
| 644 | `      document.getElementById('request-result').innerHTML = '<div class="flash flash-error text-sm">邮箱格式不正确</div>';` |
| 650 | `      document.getElementById('request-result').innerHTML = '<div class="flash flash-error text-sm">请选择寄送地址</div>';` |
| 662 | `    showMsg('申请已提交', delivery === 'email' ? '电子发票将在开具后发送至邮箱' : '纸质发票将邮寄至所选地址');` |
| 665 | `    document.getElementById('request-result').innerHTML = '<div class="flash flash-error text-sm">' + (r.error?.message \|\| '提交失败') + '</div>';` |

## landing.html (68 处)

| 行号 | 代码 |
|------|------|
| 56 | `  <div class="hero-badge"><span class="pulse-dot"></span><span style="font-size:.9rem;color:var(--text-muted)">新用户注册即送 ¥10 免费额度</span></div>` |
| 57 | `  <h1 class="hero-title">一个 API 接入<br><span class="gradient">所有主流 AI 模型</span></h1>` |
| 58 | `  <p class="hero-subtitle">统一接口、按量计费、企业级安全。用标准 OpenAI 格式调用 Claude、GPT、Gemini 等数十种模型，无需分别对接。</p>` |
| 60 | `    <a href="/register" class="btn btn-primary btn-lg">免费注册</a>` |
| 61 | `    <a href="/login" class="btn btn-secondary btn-lg">登录</a>` |
| 68 | `    <div class="stat-item"><div class="stat-value">99.9%</div><div class="stat-label">服务可用性</div></div>` |
| 69 | `    <div class="stat-item"><div class="stat-value">&lt;100ms</div><div class="stat-label">平均延迟</div></div>` |
| 70 | `    <div class="stat-item"><div class="stat-value">50+</div><div class="stat-label">可用模型</div></div>` |
| 71 | `    <div class="stat-item"><div class="stat-value">10M+</div><div class="stat-label">日处理请求</div></div>` |
| 77 | `  <h2 class="section-title">快速开始</h2>` |
| 78 | `  <p class="section-subtitle">只需三步，即可开始使用</p>` |
| 84 | `      <div><h3>注册账号</h3><p class="step-desc">创建您的 WOODGIT 账号</p></div>` |
| 89 | `          <p style="color:var(--text-muted);margin-bottom:16px">访问 <a href="/register" style="color:var(--primary)">注册页面</a>，使用邮箱即可快速注册。</p>` |
| 90 | `          <div class="check-item"><span class="check-icon">&#10003;</span>无需绑定手机号</div>` |
| 91 | `          <div class="check-item"><span class="check-icon">&#10003;</span>无需信用卡</div>` |
| 92 | `          <div class="check-item"><span class="check-icon">&#10003;</span>新用户送 ¥10 免费额度</div>` |
| 93 | `          <div class="check-item"><span class="check-icon">&#10003;</span>支持邀请码额外奖励</div>` |
| 96 | `          <div style="font-size:.8rem;color:var(--text-muted);margin-bottom:8px">免费额度支持</div>` |
| 98 | `            <div style="display:flex;justify-content:space-between;margin-bottom:8px"><span>Anthropic</span><span style="color:var(--primary)">Claude ` |
| 99 | `            <div style="display:flex;justify-content:space-between;margin-bottom:8px"><span>OpenAI</span><span style="color:var(--primary)">GPT 系列</sp` |
| 100 | `            <div style="display:flex;justify-content:space-between;margin-bottom:8px"><span>Google</span><span style="color:var(--primary)">Gemini 系列<` |
| 101 | `            <div style="display:flex;justify-content:space-between;margin-bottom:8px"><span>DeepSeek</span><span style="color:var(--primary)">DeepSeek` |
| 113 | `      <div><h3>创建 API Key</h3><p class="step-desc">获取您的访问凭证</p></div>` |
| 118 | `          <p style="color:var(--text-muted);margin-bottom:16px">进入 <a href="/apikeys" style="color:var(--primary)">API Keys 页面</a>，点击"创建新 Key"。</p>` |
| 119 | `          <div style="margin-bottom:12px"><div style="font-weight:600;margin-bottom:4px">Key 名称</div><div style="font-size:.9rem;color:var(--text-mute` |
| 120 | `          <div style="margin-bottom:12px"><div style="font-weight:600;margin-bottom:4px">服务等级</div><div style="font-size:.9rem;color:var(--text-muted)` |
| 121 | `          <div><div style="font-weight:600;margin-bottom:4px">访问模式</div><div style="font-size:.9rem;color:var(--text-muted)">单模型、多模型轮换、自动负载均衡</div></d` |
| 124 | `          <div style="font-size:.8rem;color:var(--text-muted);margin-bottom:8px">您的 API Key</div>` |
| 126 | `          <div class="warning-box">&#9888;&#65039; 请妥善保管 API Key，不要泄露给他人</div>` |
| 136 | `      <div><h3>发起请求</h3><p class="step-desc">使用 OpenAI 兼容接口调用模型</p></div>` |
| 139 | `      <p style="color:var(--text-muted);margin-bottom:16px">使用标准的 OpenAI 格式发起请求，只需修改 base URL 和 API Key。</p>` |
| 210 | `  <h2 class="section-title">核心优势</h2>` |
| 211 | `  <p class="section-subtitle">为开发者和企业打造的 AI API 中转平台</p>` |
| 213 | `    <div class="feature-item"><div class="f-icon"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" s` |
| 214 | `    <div class="feature-item"><div class="f-icon"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" s` |
| 215 | `    <div class="feature-item"><div class="f-icon"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" s` |
| 216 | `    <div class="feature-item"><div class="f-icon"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" s` |
| 217 | `    <div class="feature-item"><div class="f-icon"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" s` |
| 218 | `    <div class="feature-item"><div class="f-icon"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" s` |
| 225 | `    <h2 style="font-size:1.6rem;font-weight:700;margin-bottom:12px">立即开始</h2>` |
| 226 | `    <p style="color:var(--text-muted);margin-bottom:24px">注册即可获得免费测试额度，无需手机号，无需信用卡</p>` |
| 228 | `      <a href="/register" class="btn btn-primary btn-lg">免费注册</a>` |
| 229 | `      <a href="/login" class="btn btn-secondary btn-lg">登录</a>` |
| 237 | `    <a href="#" onclick="showPrivacyModal()">隐私政策</a>` |
| 238 | `    <a href="#" onclick="showTermsModal()">服务条款</a>` |
| 240 | `  <p>&copy; 2026 WOODGIT &mdash; AI API 中转平台</p>` |
| 247 | `      <span class="modal-title">隐私政策</span>` |
| 251 | `      <p style="background:var(--primary-light);padding:12px;border-radius:8px;margin-bottom:16px;color:var(--text)">WOODGIT 隐私政策 &mdash; 最后更新：2026年5月` |
| 252 | `      <h3 style="color:var(--text);margin-bottom:8px">我们收集哪些信息</h3>` |
| 253 | `      <p style="margin-bottom:16px">我们坚持最小必要原则，仅收集提供服务所需的信息：注册邮箱、账户名称、API 使用数据。我们不会收集不必要的个人信息。</p>` |
| 254 | `      <h3 style="color:var(--text);margin-bottom:8px">我们如何使用您的信息</h3>` |
| 255 | `      <p style="margin-bottom:16px">您的信息仅用于：提供和改善服务、计费和用量统计、安全防护和反滥用。我们绝不将您的 API 业务数据用于模型训练或其他商业目的。</p>` |
| 256 | `      <h3 style="color:var(--text);margin-bottom:8px">数据安全</h3>` |
| 257 | `      <p style="margin-bottom:16px">我们采用行业领先的加密技术（AES-256）和安全措施保护您的数据。API Key 加密存储，传输全程 TLS 加密，审计日志保留 7 年。</p>` |
| 258 | `      <h3 style="color:var(--text);margin-bottom:8px">您的权利</h3>` |
| 259 | `      <p>您可以随时：查看和导出您的数据、删除您的账户和数据、撤回授权同意。如有疑问请联系 support@example.com。</p>` |
| 262 | `      <button class="btn btn-primary" onclick="closePrivacyModal()">我已了解</button>` |
| 271 | `      <span class="modal-title">服务条款</span>` |
| 275 | `      <p style="background:var(--primary-light);padding:12px;border-radius:8px;margin-bottom:16px;color:var(--text)">WOODGIT 服务条款 &mdash; 最后更新：2026年5月` |
| 276 | `      <h3 style="color:var(--text);margin-bottom:8px">服务说明</h3>` |
| 277 | `      <p style="margin-bottom:16px">WOODGIT 提供 AI API 中转服务，将用户请求转发至上游 AI 提供商。我们致力于提供稳定、安全、合规的服务。</p>` |
| 278 | `      <h3 style="color:var(--text);margin-bottom:8px">使用规范</h3>` |
| 279 | `      <p style="margin-bottom:16px">用户应遵守法律法规，不得利用服务从事违法活动。禁止滥用 API（如恶意刷量、攻击他人服务），违规账户将被暂停。</p>` |
| 280 | `      <h3 style="color:var(--text);margin-bottom:8px">计费与退款</h3>` |
| 281 | `      <p style="margin-bottom:16px">按 Token 实际使用量计费，实时扣费。充值余额不支持退款，兑换码一经使用不可退回。详细计费规则请查看计费明细页面。</p>` |
| 282 | `      <h3 style="color:var(--text);margin-bottom:8px">免责声明</h3>` |
| 283 | `      <p>上游 AI 提供商的服务可用性和响应质量不在 WOODGIT 控制范围内。我们将尽最大努力保障服务稳定，但不对上游故障导致的损失承担责任。</p>` |
| 286 | `      <button class="btn btn-primary" onclick="closeTermsModal()">我已了解</button>` |

## layout.html (19 处)

| 行号 | 代码 |
|------|------|
| 29 | `    window._i18nMessages = {"提示": "Notice", "错误": "Error", "确认": "Confirm", "成功": "Success", "创建成功": "Created successfully", "创建失败": "Create failed", ` |
| 209 | `        <a href="/admin/models" class="sidebar-subitem {{if or (eq .Page "admin-models-general") (eq .Page "admin-models")}}active{{end}}"><svg width=` |
| 210 | `        <a href="/admin/models-aws" class="sidebar-subitem {{if eq .Page "admin-models-aws"}}active{{end}}"><svg width="16" height="16" viewBox="0 0 2` |
| 241 | `        <a href="/enterprise/groups" class="sidebar-subitem {{if eq .Page "enterprise/groups"}}active{{end}}"><svg width="16" height="16" viewBox="0 0` |
| 243 | `        <a href="/enterprise/billing" class="sidebar-subitem {{if eq .Page "enterprise/billing"}}active{{end}}"><svg width="16" height="16" viewBox="0` |
| 244 | `        <a href="/enterprise/usage" class="sidebar-subitem {{if eq .Page "enterprise/usage"}}active{{end}}"><svg width="16" height="16" viewBox="0 0 2` |
| 245 | `        <a href="/enterprise/audit" class="sidebar-subitem {{if eq .Page "enterprise/audit"}}active{{end}}"><svg width="16" height="16" viewBox="0 0 2` |
| 308 | `            <option value="zh" {{if eq .Locale "zh"}}selected{{end}}>中文</option>` |
| 344 | `          <option value="zh" {{if eq .Locale "zh"}}selected{{end}}>中文</option>` |
| 361 | `        <span class="modal-title" id="global-message-title">提示</span>` |
| 368 | `        <button class="btn btn-primary" onclick="closeGlobalModal('global-message-modal')">确定</button>` |
| 377 | `        <span class="modal-title" id="global-confirm-title">确认</span>` |
| 384 | `        <button class="btn btn-secondary" onclick="closeGlobalModal('global-confirm-modal')">取消</button>` |
| 385 | `        <button class="btn btn-danger" id="global-confirm-btn" onclick="executeGlobalConfirm()">确认</button>` |
| 394 | `        <span class="modal-title" id="ann-popup-title">公告</span>` |
| 401 | `        <button class="btn btn-secondary" onclick="closeAnnouncementPopup()">我知道了</button>` |
| 414 | `    document.getElementById('global-message-title').textContent = _t(title \|\| '提示');` |
| 430 | `    document.getElementById('global-confirm-title').textContent = _t(title \|\| '确认');` |
| 446 | `    document.getElementById('ann-popup-title').textContent = ann.title \|\| '公告';` |

## my-group.html (19 处)

| 行号 | 代码 |
|------|------|
| 9 | `  <p class="text-muted">当前所属: <span class="text-primary font-medium">{{$d.UserGroupName}}</span></p>` |
| 16 | `    <h2 class="card-title">个人可用分组</h2>` |
| 23 | `          <th>名称</th>` |
| 24 | `          <th>类型</th>` |
| 25 | `          <th>倍率</th>` |
| 28 | `          <th>模型</th>` |
| 36 | `            {{if eq .ID $d.UserGroupID}}<span class="badge badge-info" style="margin-left:6px">当前</span>{{end}}` |
| 39 | `            {{if eq .GroupType "economy"}}折扣{{else if eq .GroupType "subscription"}}套餐{{else}}标准{{end}}` |
| 56 | `            {{else}}<span class="text-muted text-sm">全部模型 ({{.TotalModels}})</span>{{end}}` |
| 64 | `  <p class="text-muted text-center mt-4">暂无可用的个人分组</p>` |
| 72 | `    <h2 class="card-title">企业分组</h2>` |
| 79 | `          <th>名称</th>` |
| 82 | `          <th>模型</th>` |
| 96 | `            {{else}}<span class="text-muted text-sm">全部模型 ({{.TotalModels}})</span>{{end}}` |
| 104 | `  <p class="text-muted text-center mt-4">暂无可用的企业分组</p>` |
| 111 | `    <h2 class="card-title">说明</h2>` |
| 114 | `    <p><strong>个人分组</strong>：平台提供的公开分组，决定计费方式和折扣。</p>` |
| 115 | `    <p><strong>企业分组</strong>：企业内部管理的分组，由企业管理员配置模型和调用限制。企业分组不计倍率（统一使用企业定价）。</p>` |
| 116 | `    <p>如需调整分组，请联系管理员。</p>` |

## my-subscriptions.html (52 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <h1 class="card-title">我的订阅</h1>` |
| 6 | `    <a href="/subscriptions" class="btn btn-primary">购买套餐</a>` |
| 13 | `    <div class="stat-label">活跃订阅</div>` |
| 19 | `        <span x-text="stat.type === 'token' ? 'Token 额度' : '按次额度'"></span>` |
| 20 | `        <small class="text-muted" x-text="'已用 ' + formatQuota(stat.used) + ' / 总计 ' + formatQuota(stat.total)"></small>` |
| 29 | `  <h2 class="card-title">订阅详情</h2>` |
| 32 | `    <div class="text-center py-8 text-muted">加载中...</div>` |
| 37 | `      <p class="text-muted">暂无订阅记录</p>` |
| 38 | `      <a href="/subscriptions" class="btn btn-primary mt-4">查看套餐</a>` |
| 48 | `            <span class="sub-type" x-text="sub.plan_type === 'token' ? 'Token套餐' : '按次套餐'"></span>` |
| 65 | `            <span class="sub-detail-label">开始时间</span>` |
| 69 | `            <span class="sub-detail-label">到期时间</span>` |
| 70 | `            <span x-text="formatDate(sub.expires_at) \|\| '永久有效'"></span>` |
| 74 | `              <span class="sub-detail-label">今日已用</span>` |
| 82 | `            <button class="btn btn-sm btn-primary" @click="renewSub(sub)">续费</button>` |
| 85 | `            <button class="btn btn-sm btn-error" @click="openCancelSubModal(sub)">取消订阅</button>` |
| 97 | `      <span class="modal-title">续费订阅</span>` |
| 103 | `          <span>套餐</span>` |
| 107 | `          <span>续费金额</span>` |
| 113 | `        <label class="form-label">选择支付方式</label>` |
| 117 | `            <span>支付宝</span>` |
| 121 | `            <span>微信支付</span>` |
| 135 | `      <button class="btn btn-secondary" onclick="document.getElementById('renew-modal').style.display='none'">取消</button>` |
| 136 | `      <button class="btn btn-primary" @click="confirmRenew()" :disabled="processing" x-text="processing ? '处理中...' : '确认续费'"></button>` |
| 145 | `      <span class="modal-title" style="color: var(--error)">⚠️ 确认取消订阅</span>` |
| 150 | `        <strong>此操作不可撤销！</strong>` |
| 152 | `      <p style="margin-bottom: 12px;">您即将取消订阅 <strong id="cancel-sub-name"></strong>。</p>` |
| 154 | `        <li>取消后，该套餐的剩余额度将<strong>立即失效</strong>，无法继续使用。</li>` |
| 155 | `        <li>未使用的配额将<strong>按比例退款</strong>到您的账户余额。</li>` |
| 156 | `        <li>取消后<strong>不可恢复</strong>，如需再次使用请重新购买。</li>` |
| 159 | `        <div class="flex justify-between"><span class="text-muted">套餐总额</span><span id="cancel-sub-price"></span></div>` |
| 160 | `        <div class="flex justify-between"><span class="text-muted">已用配额</span><span id="cancel-sub-used"></span></div>` |
| 161 | `        <div class="flex justify-between"><span class="text-muted">未用比例</span><span id="cancel-sub-ratio"></span></div>` |
| 162 | `        <div class="flex justify-between" style="font-weight: 600; color: var(--success); border-top: 1px solid var(--border); padding-top: 8px; margi` |
| 164 | `      <p style="font-size: 0.875rem; color: var(--text-muted);">如果您只是暂时不需要，可以考虑到期后自然失效，而不是主动取消。</p>` |
| 167 | `      <button class="btn btn-secondary" onclick="closeCancelSubModal()">我再想想</button>` |
| 168 | `      <button class="btn btn-error" id="cancel-sub-confirm-btn" onclick="confirmCancelSub()">确认取消订阅</button>` |
| 394 | `        case 'active': return '活跃';` |
| 395 | `        case 'expired': return '已过期';` |
| 396 | `        case 'cancelled': return '已取消';` |
| 443 | `  btn.textContent = '处理中...';` |
| 449 | `      var refundMsg = '订阅已取消';` |
| 451 | `        refundMsg += '，退款 ' + getCurrencySymbol() + formatPrice(json.refund_quota) + ' 已退回余额';` |
| 456 | `      showToast('error', json.error?.message \|\| '取消失败');` |
| 458 | `      btn.textContent = '确认取消订阅';` |
| 461 | `    showToast('error', '请求失败: ' + e.message);` |
| 463 | `    btn.textContent = '确认取消订阅';` |
| 477 | `        showToast('error', '请选择要续费的套餐');` |
| 487 | `          showToast('error', renewJson.error?.message \|\| '续费失败');` |
| 506 | `          window.location.href = '/subscriptions?toast=success&message=支付成功';` |
| 508 | `          showToast('error', paymentJson.error?.message \|\| '支付链接获取失败');` |
| 511 | `        showToast('error', '请求失败: ' + e.message);` |

## orders.html (83 处)

| 行号 | 代码 |
|------|------|
| 7 | `      导出` |
| 14 | `      <label class="filter-label">下单时间</label>` |
| 16 | `        <input type="date" id="filter-start-date" class="form-input form-input-sm" placeholder="开始日期">` |
| 17 | `        <span class="filter-separator">至</span>` |
| 18 | `        <input type="date" id="filter-end-date" class="form-input form-input-sm" placeholder="结束日期">` |
| 22 | `      <label class="filter-label">订单状态</label>` |
| 24 | `        <option value="">全部</option>` |
| 25 | `        <option value="pending">待支付</option>` |
| 26 | `        <option value="completed">已完成</option>` |
| 27 | `        <option value="cancelled">已取消</option>` |
| 31 | `      <button class="btn btn-primary btn-sm" onclick="applyFilters()">筛选</button>` |
| 32 | `      <button class="btn btn-secondary btn-sm" onclick="resetFilters()">重置</button>` |
| 38 | `    <div class="text-center py-8 text-muted" id="loading-indicator">加载中...</div>` |
| 39 | `    <div class="text-center py-8 text-muted" id="empty-indicator" style="display:none;">暂无订单</div>` |
| 47 | `            <th>支付方式</th>` |
| 49 | `            <th>操作</th>` |
| 59 | `    <button class="btn btn-sm btn-secondary" id="prev-btn" onclick="changePage(-1)" disabled>上一页</button>` |
| 60 | `    <button class="btn btn-sm btn-secondary" id="next-btn" onclick="changePage(1)">下一页</button>` |
| 68 | `      <span class="modal-title">编辑订单</span>` |
| 73 | `        <label class="form-label">订单号</label>` |
| 77 | `        <label class="form-label">金额 ($)</label>` |
| 81 | `        <label class="form-label">支付方式</label>` |
| 83 | `          <option value="alipay">支付宝</option>` |
| 84 | `          <option value="wechat">微信支付</option>` |
| 91 | `      <button class="btn btn-secondary" onclick="closeEditModal()">取消</button>` |
| 92 | `      <button class="btn btn-primary" id="edit-save-btn" onclick="saveEdit()">保存</button>` |
| 93 | `      <button class="btn btn-success" id="edit-pay-btn" onclick="saveEditAndPay()">保存并去支付</button>` |
| 102 | `      <span class="modal-title">取消订单</span>` |
| 106 | `      <p class="text-center" style="padding: 16px 0; font-size: 0.9375rem;">确定要取消该订单吗？</p>` |
| 107 | `      <p class="text-center text-sm text-muted">取消后退款将原路退回账户余额</p>` |
| 110 | `      <button class="btn btn-secondary" onclick="closeCancelModal()">再想想</button>` |
| 111 | `      <button class="btn btn-error" id="cancel-confirm-btn" onclick="confirmCancel()">确认取消</button>` |
| 120 | `      <span class="modal-title">订单详情</span>` |
| 124 | `      <div class="detail-row"><span class="detail-label">订单号</span><span id="detail-order-id" class="detail-value font-mono"></span></div>` |
| 125 | `      <div class="detail-row"><span class="detail-label">金额</span><span id="detail-amount" class="detail-value"></span></div>` |
| 126 | `      <div class="detail-row"><span class="detail-label">状态</span><span id="detail-status" class="detail-value"></span></div>` |
| 127 | `      <div class="detail-row"><span class="detail-label">支付方式</span><span id="detail-payment-method" class="detail-value"></span></div>` |
| 128 | `      <div class="detail-row"><span class="detail-label">产品类型</span><span id="detail-product-type" class="detail-value"></span></div>` |
| 129 | `      <div class="detail-row"><span class="detail-label">创建时间</span><span id="detail-created-at" class="detail-value"></span></div>` |
| 130 | `      <div class="detail-row"><span class="detail-label">完成时间</span><span id="detail-completed-at" class="detail-value"></span></div>` |
| 133 | `      <button class="btn btn-secondary" onclick="closeDetailModal()">关闭</button>` |
| 271 | `    'pending': '待支付',` |
| 272 | `    'completed': '已完成',` |
| 273 | `    'cancelled': '已取消'` |
| 289 | `    'balance': '余额',` |
| 290 | `    'alipay': '支付宝',` |
| 291 | `    'wechat': '微信支付',` |
| 292 | `    'wechat_pay': '微信支付',` |
| 303 | `    'recharge': '充值',` |
| 304 | `    'subscription': '订阅',` |
| 325 | `    btns.push('<button class="btn btn-sm btn-primary" onclick="openEditModal(\'' + order.id + '\')">编辑</button>');` |
| 326 | `    btns.push('<button class="btn btn-sm btn-success" onclick="payOrder(\'' + order.id + '\')">去支付</button>');` |
| 327 | `    btns.push('<button class="btn btn-sm btn-error" onclick="cancelOrder(\'' + order.id + '\')">取消</button>');` |
| 329 | `    btns.push('<button class="btn btn-sm btn-secondary" onclick="openDetailModal(\'' + order.id + '\')">查看详情</button>');` |
| 377 | `      document.getElementById('page-info').textContent = '第 ' + currentPage + ' 页 / 共 ' + totalPages + ' 页';` |
| 384 | `    emptyEl.textContent = '加载失败，请重试';` |
| 391 | `    if (!json.order) { showToast('error', '订单不存在'); return; }` |
| 393 | `    if (order.status !== 'pending') { showToast('error', '只有待支付订单可以编辑'); return; }` |
| 401 | `  }).catch(function(e) { showToast('error', '加载订单失败'); });` |
| 412 | `  if (!amountYuan \|\| amountYuan <= 0) { showToast('error', '请输入有效金额'); return; }` |
| 418 | `  document.getElementById('edit-save-btn').textContent = '保存中...';` |
| 426 | `    else { showToast('error', json.error?.message \|\| '保存失败'); }` |
| 427 | `  }).catch(function(e) { showToast('error', '请求失败'); });` |
| 430 | `  document.getElementById('edit-save-btn').textContent = '保存';` |
| 436 | `  if (!amountYuan \|\| amountYuan <= 0) { showToast('error', '请输入有效金额'); return; }` |
| 442 | `  document.getElementById('edit-pay-btn').textContent = '处理中...';` |
| 450 | `      showToast('error', json.error?.message \|\| '保存失败');` |
| 452 | `      document.getElementById('edit-pay-btn').textContent = '保存并去支付';` |
| 456 | `  }).catch(function(e) { showToast('error', '请求失败'); });` |
| 459 | `  document.getElementById('edit-pay-btn').textContent = '保存并去支付';` |
| 465 | `    if (!json.order) { showToast('error', '订单不存在'); return; }` |
| 466 | `    if (json.order.status !== 'pending') { showToast('error', '该订单已无法支付'); return; }` |
| 468 | `  }).catch(function(e) { showToast('error', '请求失败'); });` |
| 478 | `    else if (json.success) { window.location.href = '/orders?toast=success&message=支付成功'; }` |
| 479 | `    else { showToast('error', json.error?.message \|\| '支付链接获取失败，请稍后重试'); }` |
| 480 | `  }).catch(function(e) { showToast('error', '请求失败'); });` |
| 498 | `  document.getElementById('cancel-confirm-btn').textContent = '取消中...';` |
| 501 | `    else { showToast('error', json.error?.message \|\| '取消失败'); }` |
| 502 | `  }).catch(function(e) { showToast('error', '请求失败'); });` |
| 504 | `  document.getElementById('cancel-confirm-btn').textContent = '确认取消';` |
| 510 | `    if (!json.order) { showToast('error', '订单不存在'); return; }` |
| 521 | `  }).catch(function(e) { showToast('error', '加载订单详情失败'); });` |
| 548 | `  }).catch(function(e) { showToast('error', '导出失败，请重试'); });` |

## profile.html (109 处)

| 行号 | 代码 |
|------|------|
| 13 | `        {{if eq $p.Role "super_admin"}}超级管理员{{else if eq $p.Role "system_admin"}}系统管理员{{else if eq $p.Role "enterprise_admin"}}企业管理员{{else}}普通用户{{end}` |
| 17 | `      <button class="btn btn-secondary btn-sm" onclick="openPasswordModal()">修改密码</button>` |
| 25 | `    <h2 class="card-title">偏好设置</h2>` |
| 29 | `      <span class="text-sm font-medium text-muted">主题</span>` |
| 31 | `        <button class="btn btn-sm" :class="theme === 'light' ? 'btn-primary' : 'btn-outline'" @click="setTheme('light')">亮色</button>` |
| 32 | `        <button class="btn btn-sm" :class="theme === 'dark' ? 'btn-primary' : 'btn-outline'" @click="setTheme('dark')">暗色</button>` |
| 33 | `        <button class="btn btn-sm" :class="theme === 'system' ? 'btn-primary' : 'btn-outline'" @click="setTheme('system')">跟随系统</button>` |
| 37 | `      <span class="text-sm font-medium text-muted">语言</span>` |
| 39 | `        <option value="zh">中文</option>` |
| 44 | `      <span class="text-sm font-medium text-muted">货币</span>` |
| 51 | `      <span class="text-sm font-medium text-muted">时区</span>` |
| 74 | `      <h2 class="card-title">基本信息</h2>` |
| 78 | `        <label class="form-label">姓名</label>` |
| 79 | `        <input type="text" class="form-input" x-model="name" value="{{$p.Name}}" placeholder="您的姓名">` |
| 82 | `        <label class="form-label">公司</label>` |
| 83 | `        <input type="text" class="form-input" x-model="company" value="{{$p.Company}}" placeholder="公司名称" autocomplete="off">` |
| 87 | `        <button class="btn btn-primary btn-sm" @click="submit()">保存</button>` |
| 95 | `      <h2 class="card-title">联系方式</h2>` |
| 102 | `          <span class="text-xs text-muted">邮箱</span>` |
| 105 | `        <button class="btn btn-ghost btn-sm" onclick="openContactModal('email')">修改</button>` |
| 111 | `          <span class="text-xs text-muted">备用邮箱</span>` |
| 112 | `          <div class="text-sm font-medium">{{if $p.SecondEmail}}{{$p.SecondEmail}}{{else}}<span class="text-muted">未设置</span>{{end}}</div>` |
| 114 | `        <button class="btn btn-ghost btn-sm" onclick="openContactModal('backup_email')">{{if $p.SecondEmail}}修改{{else}}添加{{end}}</button>` |
| 120 | `          <span class="text-xs text-muted">手机号码</span>` |
| 121 | `          <div class="text-sm font-medium">{{if $p.Phone}}{{$p.Phone}}{{else}}<span class="text-muted">未设置</span>{{end}}</div>` |
| 123 | `        <button class="btn btn-ghost btn-sm" onclick="openContactModal('phone')">{{if $p.Phone}}修改{{else}}添加{{end}}</button>` |
| 132 | `    <h2 class="card-title text-error">危险操作</h2>` |
| 138 | `        <h3 class="font-medium text-sm">导出个人数据 (GDPR)</h3>` |
| 139 | `        <p class="text-xs text-muted mt-1">下载您的所有个人数据（账户、API密钥、使用记录、订单）。</p>` |
| 141 | `      <button class="btn btn-secondary btn-sm" onclick="openExportModal()">导出数据</button>` |
| 146 | `        <h3 class="font-medium text-sm">请求删除数据 (GDPR)</h3>` |
| 147 | `        <p class="text-xs text-muted mt-1">删除所有个人数据并停用账户。不可逆。</p>` |
| 149 | `      <button class="btn btn-warning btn-sm" onclick="openGDPRDeleteModal()">请求删除</button>` |
| 154 | `        <h3 class="font-medium text-error text-sm">删除账户</h3>` |
| 155 | `        <p class="text-xs text-muted mt-1">永久删除账户，不可撤销。</p>` |
| 157 | `      <button class="btn btn-danger btn-sm" onclick="openDeleteAccountModal()">删除账户</button>` |
| 168 | `      <span class="modal-title">修改密码</span>` |
| 173 | `        <p class="text-xs font-medium">密码要求：</p>` |
| 175 | `          <li>至少 8 位字符</li>` |
| 176 | `          <li>包含大写字母 (A-Z)</li>` |
| 177 | `          <li>包含小写字母 (a-z)</li>` |
| 178 | `          <li>包含数字 (0-9)</li>` |
| 179 | `          <li>包含特殊字符 (!@#$%^&* 等)</li>` |
| 183 | `        <label class="form-label">当前密码</label>` |
| 184 | `        <input type="password" id="pw-current" class="form-input" placeholder="输入当前密码">` |
| 187 | `        <label class="form-label">新密码</label>` |
| 188 | `        <input type="password" id="pw-new" class="form-input" placeholder="至少8位" oninput="checkPwMatch()">` |
| 191 | `        <label class="form-label">确认新密码</label>` |
| 192 | `        <input type="password" id="pw-confirm" class="form-input" placeholder="再次输入新密码" oninput="checkPwMatch()">` |
| 193 | `        <p class="text-xs text-error mt-1" id="pw-match-hint" style="display:none">两次密码不一致</p>` |
| 198 | `      <button class="btn btn-secondary" onclick="document.getElementById('password-modal').style.display='none'">取消</button>` |
| 199 | `      <button class="btn btn-primary" id="pw-submit-btn" onclick="submitPassword()" disabled>确认修改</button>` |
| 208 | `      <span class="modal-title" id="contact-modal-title">修改</span>` |
| 213 | `        <label class="form-label" id="contact-value-label">新值</label>` |
| 216 | `          <button type="button" class="btn btn-secondary btn-sm" id="contact-send-code" onclick="sendContactCode()">发送验证码</button>` |
| 220 | `        <label class="form-label">验证码</label>` |
| 221 | `        <input type="text" id="contact-code" class="form-input" maxlength="6" placeholder="6位验证码">` |
| 224 | `        <label class="form-label">当前密码</label>` |
| 225 | `        <input type="password" id="contact-password" class="form-input" placeholder="输入登录密码确认">` |
| 230 | `      <button class="btn btn-secondary" onclick="document.getElementById('contact-modal').style.display='none'">取消</button>` |
| 231 | `      <button class="btn btn-primary" onclick="submitContactChange()">确认修改</button>` |
| 240 | `      <span class="modal-title">导出个人数据</span>` |
| 244 | `      <p class="text-sm text-muted mb-4">为保护您的数据安全，导出前需要验证密码。</p>` |
| 246 | `        <label class="form-label">当前密码</label>` |
| 247 | `        <input type="password" id="export-password" class="form-input" placeholder="输入登录密码">` |
| 252 | `      <button class="btn btn-secondary" onclick="document.getElementById('export-modal').style.display='none'">取消</button>` |
| 253 | `      <button class="btn btn-primary" onclick="executeExport()">确认导出</button>` |
| 262 | `      <span class="modal-title text-error">确认删除数据</span>` |
| 267 | `        <p class="text-xs"><strong class="text-error">此操作将：</strong></p>` |
| 269 | `          <li>停用所有 API 密钥</li>` |
| 270 | `          <li>删除所有使用记录</li>` |
| 271 | `          <li>永久停用账户</li>` |
| 275 | `        <label class="form-label">输入 <code class="text-error">DELETE_MY_DATA</code></label>` |
| 279 | `        <label class="form-label">密码</label>` |
| 284 | `      <button class="btn btn-secondary" onclick="document.getElementById('gdpr-delete-modal').style.display='none'">取消</button>` |
| 285 | `      <button class="btn btn-danger" id="gdpr-delete-btn" onclick="executeGDPRDelete()" disabled>永久删除</button>` |
| 294 | `      <span class="modal-title text-error">删除账户</span>` |
| 299 | `        <p class="text-xs"><strong class="text-error">警告：</strong>此操作不可撤销。邮箱将被匿名化，API 密钥将被停用。</p>` |
| 302 | `        <label class="form-label">输入 <code class="text-error">DELETE</code></label>` |
| 306 | `        <label class="form-label">密码</label>` |
| 311 | `      <button class="btn btn-secondary" onclick="document.getElementById('delete-account-modal').style.display='none'">取消</button>` |
| 312 | `      <button class="btn btn-danger" id="delete-account-btn" onclick="executeDeleteAccount()" disabled>永久删除</button>` |
| 401 | `          ? '<span class="text-xs text-success">已保存</span>'` |
| 402 | `          : '<span class="text-xs text-error">' + (r.error?.message \|\| '失败') + '</span>';` |
| 404 | `        document.getElementById('profile-result').innerHTML = '<span class="text-xs text-error">保存失败</span>';` |
| 448 | `      document.getElementById('pw-result').innerHTML = '<div class="flash flash-success text-sm">密码已修改</div>';` |
| 451 | `      document.getElementById('pw-result').innerHTML = '<div class="flash flash-error text-sm">' + (r.error?.message \|\| '修改失败') + '</div>';` |
| 454 | `    document.getElementById('pw-result').innerHTML = '<div class="flash flash-error text-sm">请求失败</div>';` |
| 472 | `  if (type === 'email') { title.textContent = '修改邮箱'; label.textContent = '新邮箱'; input.placeholder = 'new@example.com'; input.type = 'email'; }` |
| 473 | `  else if (type === 'backup_email') { title.textContent = '修改备用邮箱'; label.textContent = '备用邮箱'; input.placeholder = 'backup@example.com'; input.type =` |
| 474 | `  else { title.textContent = '修改手机号'; label.textContent = '手机号'; input.placeholder = '+86 138 0000 0000'; input.type = 'tel'; }` |
| 493 | `      var timer = setInterval(() => { contactCooldown--; btn.textContent = contactCooldown > 0 ? contactCooldown + 's' : '发送验证码'; btn.disabled = conta` |
| 495 | `      document.getElementById('contact-result').innerHTML = '<div class="flash flash-error text-sm">' + (r.error?.message \|\| '发送失败') + '</div>';` |
| 498 | `    document.getElementById('contact-result').innerHTML = '<div class="flash flash-error text-sm">发送失败</div>';` |
| 506 | `  if (!value \|\| !code \|\| !password) { document.getElementById('contact-result').innerHTML = '<div class="flash flash-error text-sm">请填写所有字段</div>'` |
| 515 | `      document.getElementById('contact-result').innerHTML = '<div class="flash flash-success text-sm">修改成功</div>';` |
| 518 | `      document.getElementById('contact-result').innerHTML = '<div class="flash flash-error text-sm">' + (r.error?.message \|\| '修改失败') + '</div>';` |
| 521 | `    document.getElementById('contact-result').innerHTML = '<div class="flash flash-error text-sm">请求失败</div>';` |
| 534 | `  if (!password) { document.getElementById('export-result').innerHTML = '<div class="flash flash-error text-sm">请输入密码</div>'; return; }` |
| 543 | `    if (!vr.token) { document.getElementById('export-result').innerHTML = '<div class="flash flash-error text-sm">密码错误</div>'; return; }` |
| 546 | `    document.getElementById('export-result').innerHTML = '<div class="text-sm text-muted">正在导出...</div>';` |
| 554 | `    document.getElementById('export-result').innerHTML = '<div class="flash flash-success text-sm">导出成功，文件已下载</div>';` |
| 556 | `    document.getElementById('export-result').innerHTML = '<div class="flash flash-error text-sm">导出失败</div>';` |
| 576 | `    if (!(await verify.json()).token) { showToast('error', '密码错误'); return; }` |
| 580 | `    if (r.ok) { showToast('success', '数据已删除'); window.location.href = '/login'; } else { showToast('error', r.error?.message \|\| '删除失败'); }` |
| 581 | `  } catch(e) { showToast('error', '请求失败'); }` |
| 600 | `    if (!(await verify.json()).token) { showToast('error', '密码错误'); return; }` |
| 604 | `    if (r.success) { showToast('success', '账户已删除'); window.location.href = '/login'; } else { showToast('error', r.error?.message \|\| '删除失败'); }` |
| 605 | `  } catch(e) { showToast('error', '请求失败'); }` |

## redemption.html (41 处)

| 行号 | 代码 |
|------|------|
| 7 | `      使用兑换码` |
| 14 | `      <h2 class="card-title">我的兑换记录</h2>` |
| 20 | `        <label class="filter-label">生效时间</label>` |
| 22 | `          <input type="date" id="filter-start-date" class="form-input form-input-sm" placeholder="开始日期">` |
| 23 | `          <span class="filter-separator">至</span>` |
| 24 | `          <input type="date" id="filter-end-date" class="form-input form-input-sm" placeholder="结束日期">` |
| 28 | `        <label class="filter-label">状态</label>` |
| 30 | `          <option value="">全部</option>` |
| 31 | `          <option value="active">可用</option>` |
| 32 | `          <option value="used">已兑换</option>` |
| 33 | `          <option value="expired">已过期</option>` |
| 37 | `        <button class="btn btn-primary btn-sm" onclick="applyFilters()">筛选</button>` |
| 38 | `        <button class="btn btn-secondary btn-sm" onclick="resetFilters()">重置</button>` |
| 43 | `      <div id="loading-indicator" class="text-center py-8 text-muted" style="display:none;">加载中...</div>` |
| 44 | `      <div id="empty-indicator" class="text-center py-8 text-muted" style="display:none;">暂无兑换记录</div>` |
| 49 | `              <th>兑换码</th>` |
| 50 | `              <th>类型</th>` |
| 51 | `              <th>金额/额度</th>` |
| 52 | `              <th>状态</th>` |
| 53 | `              <th>生效时间</th>` |
| 54 | `              <th>过期时间</th>` |
| 69 | `      <span class="modal-title">使用兑换码</span>` |
| 74 | `        <label class="form-label">请输入兑换码</label>` |
| 75 | `        <input type="text" id="redeem-code" class="form-input" placeholder="请输入兑换码" autocomplete="off">` |
| 80 | `        <span id="redeem-success-text">兑换成功！</span>` |
| 84 | `      <button class="btn btn-secondary" onclick="closeRedeemModal()">取消</button>` |
| 85 | `      <button class="btn btn-primary" id="redeem-submit-btn" onclick="submitRedeem()">确认兑换</button>` |
| 213 | `      var statusBadge = '<span class="badge badge-success">已兑换</span>';` |
| 214 | `      var typeLabel = v.type==='balance'?'余额兑换':v.type==='promo'?'促销优惠':v.type==='token_plan'?'Token Plan':v.type==='code_plan'?'Code Plan':'余额充值';` |
| 218 | `      var expiresAt = v.expires_at ? formatDate(v.expires_at) : '永久';` |
| 221 | `        amountLabel = '计划兑换';` |
| 236 | `    emptyEl.textContent = '加载失败，请重试';` |
| 265 | `  document.getElementById('redeem-submit-btn').textContent = '确认兑换';` |
| 281 | `    errorEl.textContent = '请输入兑换码';` |
| 289 | `  submitBtn.textContent = '兑换中...';` |
| 300 | `      successText.textContent = json.message \|\| '兑换成功！余额已增加 ' + getCurrencySymbol() + formatPrice(json.amount \|\| 0);` |
| 302 | `      submitBtn.textContent = '兑换成功';` |
| 306 | `      errorEl.textContent = json.error?.message \|\| json.error \|\| '兑换失败，请检查兑换码是否正确';` |
| 309 | `      submitBtn.textContent = '确认兑换';` |
| 312 | `    errorEl.textContent = '请求失败，请重试';` |
| 315 | `    submitBtn.textContent = '确认兑换';` |

## register.html (1 处)

| 行号 | 代码 |
|------|------|
| 39 | `        <label class="form-label">邀请码（可选）</label>` |

## subscriptions.html (30 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <h1 class="card-title">订阅中心</h1>` |
| 6 | `    <a href="/subscriptions/my" class="btn btn-secondary">我的订阅</a>` |
| 8 | `  <p class="text-muted mt-2">选择适合您的套餐，获得更优惠的价格和更多配额</p>` |
| 14 | `    <div class="text-center py-12 text-muted">加载中...</div>` |
| 18 | `    <div class="text-center py-12 text-muted">暂无可用套餐</div>` |
| 26 | `          <span class="plan-badge">推荐</span>` |
| 45 | `            <span x-text="'每日限额 ' + formatQuota(plan.daily_limit, plan.plan_type)"></span>` |
| 50 | `          <span x-text="plan.plan_type === 'token' ? 'Token 计费' : '按次计费'"></span>` |
| 58 | `        立即购买` |
| 68 | `      <span class="modal-title">购买套餐</span>` |
| 74 | `          <span>套餐名称</span>` |
| 78 | `          <span>有效期</span>` |
| 82 | `          <span>配额</span>` |
| 86 | `          <span>支付金额</span>` |
| 92 | `        <label class="form-label">选择支付方式</label>` |
| 96 | `            <span>支付宝</span>` |
| 100 | `            <span>微信支付</span>` |
| 114 | `      <button class="btn btn-secondary" onclick="document.getElementById('purchase-modal').style.display='none'">取消</button>` |
| 115 | `      <button class="btn btn-primary" @click="confirmPurchase()" :disabled="processing" x-text="processing ? '处理中...' : '确认支付'"></button>` |
| 301 | `      return window.userCurrency === 'USD' ? 'USD' : '元';` |
| 304 | `      if (!quota) return '无限制';` |
| 310 | `      return quota + ' 次';` |
| 316 | `      var quotaText = plan.quota ? (plan.plan_type === 'token' ? (plan.quota >= 1000000 ? (plan.quota / 1000000).toFixed(1) + 'M Tokens' : plan.quota ` |
| 330 | `      if (!quota) return '无限制';` |
| 336 | `      return quota + ' 次';` |
| 342 | `        showToast('error', '请选择套餐');` |
| 356 | `          showToast('error', purchaseJson.error?.message \|\| '购买失败');` |
| 375 | `          window.location.href = '/subscriptions?toast=success&message=支付成功';` |
| 377 | `          showToast('error', paymentJson.error?.message \|\| '支付链接获取失败，请稍后重试');` |
| 380 | `        showToast('error', '请求失败: ' + e.message);` |

## super/payment.html (9 处)

| 行号 | 代码 |
|------|------|
| 11 | `      导出配置` |
| 18 | `  <h2 class="card-title">支付模式</h2>` |
| 21 | `      <div class="font-medium" id="payment-mode-label">当前模式：真实支付</div>` |
| 22 | `      <p class="text-xs text-muted mt-1">模拟充值模式下，用户充值将直接成功，不经过支付网关。适用于测试环境。</p>` |
| 58 | `    label.textContent = '当前模式：模拟充值（测试）';` |
| 62 | `    label.textContent = '当前模式：真实支付';` |
| 85 | `        status.textContent = '⚠ 已切换到模拟充值模式，用户充值将直接成功。';` |
| 89 | `        status.textContent = '✓ 已切换到真实支付模式，用户充值需要通过支付网关。';` |
| 96 | `    showToast('error', '设置保存失败');` |

## super/providers.html (23 处)

| 行号 | 代码 |
|------|------|
| 15 | `          <th>名称</th>` |
| 16 | `          <th>类型</th>` |
| 18 | `          <th>状态</th>` |
| 19 | `          <th>操作</th>` |
| 29 | `          <td><span class="badge badge-{{if .Enabled}}success{{else}}error{{end}}">{{if .Enabled}}活跃{{else}}禁用{{end}}</span></td>` |
| 32 | `              <button class="btn btn-sm btn-ghost" onclick="editProvider('{{.ID}}', '{{.Name}}', '{{.Type}}', '{{.BaseURL}}')" title="编辑">` |
| 35 | `              <button class="btn btn-sm btn-secondary" onclick="toggleProvider('{{.ID}}', {{if .Enabled}}false{{else}}true{{end}})">{{if .Enabled}}禁用{` |
| 36 | `              <button class="btn btn-sm btn-danger" onclick="confirmDelete('{{.ID}}', '{{.Name}}')">删除</button>` |
| 91 | `      <span class="modal-title">编辑供应商</span>` |
| 169 | `        showMessage('添加失败', data.error.message \|\| JSON.stringify(data.error));` |
| 171 | `        showMessage('添加成功', '供应商已成功添加', function() { window.location.reload(); });` |
| 176 | `      showMessage('添加失败', err.message);` |
| 196 | `        showMessage('编辑失败', data.error.message \|\| JSON.stringify(data.error));` |
| 198 | `        showMessage('编辑成功', '供应商信息已更新', function() { window.location.reload(); });` |
| 203 | `      showMessage('编辑失败', err.message);` |
| 208 | `  var action = enable ? '启用' : '禁用';` |
| 217 | `        showMessage('操作失败', data.error.message \|\| JSON.stringify(data.error));` |
| 219 | `        showMessage('操作成功', '供应商已' + action, function() { window.location.reload(); });` |
| 222 | `    .catch(err => showMessage('操作失败', err.message));` |
| 227 | `  showConfirm('删除供应商', '确定要删除供应商 "' + providerName + '" 吗？此操作不可撤销。', doDelete);` |
| 238 | `        showMessage('删除失败', data.error.message \|\| JSON.stringify(data.error));` |
| 240 | `        showMessage('删除成功', '供应商已删除', function() { window.location.reload(); });` |
| 243 | `    .catch(err => showMessage('删除失败', err.message))` |

## enterprise/audit.html (24 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">审计日志</h1>` |
| 6 | `      <a href="/api/enterprise/audit/export?type=roles" class="btn btn-secondary btn-sm">导出角色变更</a>` |
| 7 | `      <a href="/api/enterprise/audit/export?type=credits" class="btn btn-secondary btn-sm">导出配额变更</a>` |
| 10 | `  <p class="text-muted">企业成员角色变更、配额/信用变更记录。</p>` |
| 16 | `    <h2 class="card-title">角色变更记录</h2>` |
| 23 | `          <th>时间</th>` |
| 24 | `          <th>成员</th>` |
| 25 | `          <th>变更</th>` |
| 26 | `          <th>操作人</th>` |
| 27 | `          <th>原因</th>` |
| 43 | `            {{if .changed_by_email}}{{.changed_by_email}}{{else}}系统{{end}}` |
| 55 | `    <p class="text-muted">暂无角色变更记录</p>` |
| 63 | `    <h2 class="card-title">配额/信用变更记录</h2>` |
| 70 | `          <th>时间</th>` |
| 71 | `          <th>类型</th>` |
| 72 | `          <th>字段</th>` |
| 73 | `          <th>变动</th>` |
| 74 | `          <th>操作人</th>` |
| 75 | `          <th>原因</th>` |
| 84 | `              {{if eq .change_type "quota_topup"}}配额充值` |
| 85 | `              {{else if eq .change_type "credit_topup"}}信用充值` |
| 86 | `              {{else if eq .change_type "monthly_reset"}}月度重置` |
| 87 | `              {{else if eq .change_type "admin_override"}}管理员操作` |
| 106 | `    <p class="text-muted">暂无配额/信用变更记录</p>` |

## enterprise/billing.html (17 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <h1 class="card-title">企业账单</h1>` |
| 28 | `    <h2 class="card-title">信用额度概览</h2>` |
| 32 | `      <span class="credit-gauge-label">已使用 {{printf "%.1f" $d.CreditPercent}}%</span>` |
| 40 | `        <div class="stat-label">总额度</div>` |
| 44 | `        <div class="stat-label">已消费</div>` |
| 48 | `        <div class="stat-label">剩余</div>` |
| 52 | `        <div class="stat-label">使用率</div>` |
| 96 | `    <h2 class="card-title">按模型用量</h2>` |
| 102 | `          <th>模型</th>` |
| 103 | `          <th>Token 数</th>` |
| 104 | `          <th>请求数</th>` |
| 105 | `          <th>占比</th>` |
| 127 | `    <h2 class="card-title">定价信息</h2>` |
| 131 | `      <div class="stat-label">企业定价倍率</div>` |
| 133 | `      <div class="text-xs text-muted mt-1">{{if lt $d.PricingRatio 1.0}}优惠 {{mul (sub 1.0 $d.PricingRatio) 100}}%{{else if gt $d.PricingRatio 1.0}}溢价 ` |
| 136 | `      <div class="stat-label">计费周期</div>` |
| 141 | `      <div class="stat-label">下次重置</div>` |

## enterprise/dashboard.html (38 处)

| 行号 | 代码 |
|------|------|
| 50 | `        {{if eq $d.Status "suspended"}}企业已暂停{{else if eq $d.Status "admin_vacant"}}管理员空缺{{else}}{{$d.Status}}{{end}}` |
| 53 | `        {{if eq $d.Status "suspended"}}企业运营已暂停，请联系平台管理员恢复。现有密钥仍可使用，但无法进行管理操作。` |
| 54 | `        {{else if eq $d.Status "admin_vacant"}}当前无企业管理员，请联系平台管理员指派。成员可正常使用现有密钥，但无法进行管理操作。{{end}}` |
| 71 | `        <span>成员管理</span>` |
| 75 | `        <span>企业分组</span>` |
| 79 | `        <span>密钥管理</span>` |
| 83 | `        <span>企业账单</span>` |
| 87 | `        <span>用量明细</span>` |
| 91 | `        <span>审计日志</span>` |
| 103 | `    <div class="trend trend-neutral">上限 {{$d.MaxMembers}}</div>` |
| 129 | `    <h2 class="card-title">信用额度</h2>` |
| 131 | `    <span class="text-xs text-muted">下次重置：{{formatDateTime $d.NextResetAt $.Timezone}}</span>` |
| 137 | `      <span class="credit-gauge-label">已使用 {{printf "%.1f" $d.CreditPercent}}%</span>` |
| 145 | `        <div class="stat-label">总额度</div>` |
| 149 | `        <div class="stat-label">已消费</div>` |
| 153 | `        <div class="stat-label">剩余</div>` |
| 159 | `  <p class="text-muted text-center" style="padding:1rem">未配置信用额度</p>` |
| 167 | `    <h2 class="card-title">近 30 天用量趋势</h2>` |
| 168 | `    <a href="/enterprise/usage" class="text-xs text-muted">查看详情 →</a>` |
| 179 | `      <div style="flex:1;display:flex;flex-direction:column;align-items:center;gap:2px" title="{{.Date}}: {{fmtNum .Requests}} 次, {{fmtNum .Tokens}} t` |
| 196 | `    <h2 class="card-title">告警历史</h2>` |
| 202 | `          <th>时间</th>` |
| 203 | `          <th>类型</th>` |
| 204 | `          <th>阈值</th>` |
| 205 | `          <th>当前值</th>` |
| 206 | `          <th>上限值</th>` |
| 207 | `          <th>状态</th>` |
| 216 | `              {{if eq .AlertType "quota_warning"}}配额告警` |
| 217 | `              {{else if eq .AlertType "quota_exceeded"}}配额超限` |
| 218 | `              {{else if eq .AlertType "credit_warning"}}信用告警` |
| 219 | `              {{else if eq .AlertType "credit_exceeded"}}信用超限` |
| 227 | `            {{if .AcknowledgedAt}}<span class="badge badge-success">已确认</span>` |
| 228 | `            {{else}}<span class="badge badge-warning">待处理</span>{{end}}` |
| 241 | `  <h2 class="card-title">本月用量TOP用户</h2>` |
| 246 | `          <th>用户</th>` |
| 247 | `          <th>Token数</th>` |
| 248 | `          <th>请求数</th>` |
| 249 | `          <th>占比</th>` |

## enterprise/groups.html (57 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">企业分组管理</h1>` |
| 5 | `    <button class="btn btn-primary" onclick="openCreateModal()">新建分组</button>` |
| 13 | `          <th>名称</th>` |
| 14 | `          <th>基于平台分组</th>` |
| 15 | `          <th>可见性</th>` |
| 16 | `          <th>倍率</th>` |
| 17 | `          <th>模型</th>` |
| 19 | `          <th>操作</th>` |
| 35 | `            <span class="badge badge-error">管理员专用</span>` |
| 37 | `            <span class="badge badge-success">公开</span>` |
| 43 | `            <span class="text-sm" title="企业折扣 {{printf "%.2f" .EnterpriseRatio}}x × 平台倍率 {{printf "%.2f" .ParentGroupRatio}}x = 实际倍率">` |
| 64 | `              <button class="btn btn-sm btn-secondary" onclick="openEditModal('{{.ID}}', '{{.Name}}', '{{.Models}}', {{.RPM}}, {{.TPM}})">编辑</button>` |
| 65 | `              <button class="btn btn-sm btn-danger" onclick='confirmDeleteGroup("{{.ID}}", "{{.Name}}")'>删除</button>` |
| 74 | `  <p class="text-muted text-center mt-4">暂无分组。创建一个分组以限制成员的模型访问和调用频率。</p>` |
| 82 | `      <span class="modal-title" id="group-modal-title">新建分组</span>` |
| 88 | `        <label class="form-label">名称 *</label>` |
| 89 | `        <input type="text" id="group-name" class="form-input" placeholder="例如：研发团队-生产环境" required>` |
| 92 | `        <label class="form-label">选择平台分组 *</label>` |
| 94 | `          <option value="">-- 选择平台企业分组 --</option>` |
| 99 | `        <label class="form-label">选择模型</label>` |
| 101 | `          <span class="text-muted text-sm">请先选择平台分组</span>` |
| 106 | `          <label class="form-label">RPM (每分钟请求数)</label>` |
| 112 | `        <label class="form-label">TPM (每分钟Token数)</label>` |
| 113 | `        <input type="number" id="group-tpm" class="form-input" value="0" min="0" placeholder="0 = 不限制">` |
| 114 | `        <span class="text-xs" style="color:#f59e0b;">⚠ 不建议随意配置。设为0即不限制。</span>` |
| 117 | `        <label class="form-label">分组可见性</label>` |
| 119 | `          <option value="public">公开 — 所有成员可见，成员可自行创建密钥使用</option>` |
| 120 | `          <option value="admin_only">管理员专用 — 仅管理员可分配，对成员隐藏</option>` |
| 125 | `      <button class="btn btn-secondary" onclick="closeGroupModal()">取消</button>` |
| 126 | `      <button class="btn btn-primary" id="group-save-btn" onclick="saveGroup()">创建</button>` |
| 135 | `      <span class="modal-title">删除分组</span>` |
| 139 | `      <p>确定要删除分组 <strong id="delete-group-name"></strong> 吗？</p>` |
| 142 | `      <button class="btn btn-secondary" onclick="document.getElementById('delete-group-modal').style.display='none'">取消</button>` |
| 143 | `      <button class="btn btn-danger" id="delete-group-btn">确认删除</button>` |
| 160 | `      select.innerHTML = '<option value="">-- 选择平台企业分组 --</option>';` |
| 162 | `        var modelsInfo = g.model_count > 0 ? g.model_count + ' 个模型' : '所有模型';` |
| 167 | `    .catch(function() { showToast('error', '加载平台分组失败'); });` |
| 176 | `    document.getElementById('model-checkboxes').innerHTML = '<span class="text-muted text-sm">请先选择平台分组</span>';` |
| 192 | `    if (g.rpmonth) limits.push('RP/月:' + g.rpmonth);` |
| 195 | `    if (g.tpmonth) limits.push('TP/月:' + g.tpmonth);` |
| 196 | `    detailEl.innerHTML = '平台分组详情 — 限速: ' + (limits.length ? limits.join(', ') : '无限制') +` |
| 197 | `      ' \| 倍率: ' + g.pricing_ratio + 'x \| 模型: ' + (g.model_count > 0 ? g.model_count + '个' : '全部');` |
| 202 | `  document.getElementById('model-checkboxes').innerHTML = '<span class="text-muted text-sm">加载模型列表...</span>';` |
| 209 | `    .catch(function() { showToast('error', '加载模型列表失败'); });` |
| 216 | `    container.innerHTML = '<span class="text-muted text-sm">该平台分组未限制模型（可选择所有模型）</span>';` |
| 241 | `  document.getElementById('group-modal-title').textContent = '新建分组';` |
| 242 | `  document.getElementById('group-save-btn').textContent = '创建';` |
| 243 | `  document.getElementById('model-checkboxes').innerHTML = '<span class="text-muted text-sm">请先选择平台分组</span>';` |
| 254 | `  document.getElementById('group-modal-title').textContent = '编辑分组';` |
| 255 | `  document.getElementById('group-save-btn').textContent = '保存';` |
| 275 | `  if (!name) { showToast('error', '请输入分组名称'); return; }` |
| 279 | `    showToast('error', '请选择平台分组'); return;` |
| 286 | `    showToast('error', 'RPM 不能超过平台分组的限制 (' + maxRPM + ')'); return;` |
| 313 | `    else { showToast('error', (res.error && res.error.message) \|\| '操作失败'); }` |
| 315 | `  .catch(function() { showToast('error', '请求失败'); });` |
| 331 | `        else { showToast('error', (res.error && res.error.message) \|\| '删除失败'); }` |
| 333 | `      .catch(function() { showToast('error', '请求失败'); });` |

## enterprise/keys.html (56 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">企业密钥管理</h1>` |
| 5 | `    <button class="btn btn-primary" onclick="document.getElementById('create-key-modal').style.display='flex'">创建新密钥</button>` |
| 13 | `          <th>名称</th>` |
| 14 | `          <th>密钥前缀</th>` |
| 15 | `          <th>分配给</th>` |
| 16 | `          <th>分组</th>` |
| 17 | `          <th>状态</th>` |
| 18 | `          <th>最近使用</th>` |
| 19 | `          <th>创建时间</th>` |
| 20 | `          <th>操作</th>` |
| 28 | `          <td>{{if .AssignedUserName}}{{.AssignedUserName}}{{else}}<span class="text-muted">未分配</span>{{end}}</td>` |
| 32 | `              {{if eq .Status "active"}}正常{{else}}已禁用{{end}}` |
| 36 | `            <span class="text-muted">从未使用</span>` |
| 41 | `              <button class="btn btn-sm btn-secondary" onclick="viewKey('{{.ID}}')">查看完整密钥</button>` |
| 43 | `              <button class="btn btn-sm btn-secondary" onclick="editKey('{{.ID}}', '{{.Name}}', '{{.AssignedUserID}}', '{{.GroupID}}')">编辑</button>` |
| 44 | `              <button class="btn btn-sm btn-danger" onclick="confirmRevoke('{{.ID}}', '{{.Name}}')">禁用</button>` |
| 46 | `              <button class="btn btn-sm btn-success" onclick="reactivateKey('{{.ID}}')">激活</button>` |
| 48 | `              <button class="btn btn-sm btn-danger" onclick="confirmDelete('{{.ID}}', '{{.Name}}')">删除</button>` |
| 57 | `  <p class="text-muted text-center mt-4">暂无密钥</p>` |
| 65 | `      <span class="modal-title">创建新密钥</span>` |
| 72 | `          <label class="form-label">密钥名称 *</label>` |
| 73 | `          <input type="text" name="name" class="form-input" required placeholder="例如：生产环境密钥">` |
| 76 | `          <label class="form-label">分配给成员</label>` |
| 78 | `            <option value="">不指定</option>` |
| 85 | `          <label class="form-label">选择企业分组</label>` |
| 87 | `            <option value="">不指定（使用默认方式）</option>` |
| 92 | `          <span class="text-xs text-muted mt-1">选择企业分组可限制该成员可用的模型和调用频率。</span>` |
| 96 | `        <button type="button" class="btn btn-secondary" onclick="document.getElementById('create-key-modal').style.display='none'">取消</button>` |
| 97 | `        <button type="submit" class="btn btn-primary">创建</button>` |
| 107 | `      <span class="modal-title">密钥详情</span>` |
| 111 | `      <div class="key-warning">请妥善保存以下密钥，关闭后无法再次查看完整密钥。</div>` |
| 114 | `        <button class="btn btn-sm btn-secondary" onclick="copyKey()">复制</button>` |
| 118 | `      <button class="btn btn-secondary" onclick="document.getElementById('view-key-modal').style.display='none'">关闭</button>` |
| 127 | `      <span class="modal-title">密钥创建成功</span>` |
| 131 | `      <div class="key-warning">请立即保存以下密钥，此为唯一一次查看完整密钥的机会。</div>` |
| 134 | `        <button class="btn btn-sm btn-secondary" onclick="copyNewKey()">复制</button>` |
| 138 | `      <button class="btn btn-primary" onclick="document.getElementById('new-key-modal').style.display='none'">我已保存</button>` |
| 147 | `      <span class="modal-title">编辑密钥</span>` |
| 155 | `        <label class="form-label">重新分配给</label>` |
| 163 | `        <label class="form-label">所属分组</label>` |
| 165 | `          <option value="">不指定</option>` |
| 173 | `      <button class="btn btn-secondary" onclick="document.getElementById('edit-key-modal').style.display='none'">取消</button>` |
| 174 | `      <button class="btn btn-primary" onclick="submitEditKey()">保存</button>` |
| 183 | `      <span class="modal-title">禁用密钥</span>` |
| 187 | `      <p>确定要禁用密钥 <strong id="revoke-key-name"></strong> 吗？禁用后使用该密钥的请求将被拒绝。</p>` |
| 190 | `      <button class="btn btn-secondary" onclick="document.getElementById('revoke-key-modal').style.display='none'">取消</button>` |
| 193 | `        <button type="submit" class="btn btn-danger">确认禁用</button>` |
| 203 | `      <span class="modal-title">删除密钥</span>` |
| 207 | `      <p>确定要删除密钥 <strong id="delete-key-name"></strong> 吗？此操作不可恢复。</p>` |
| 210 | `      <button class="btn btn-secondary" onclick="document.getElementById('delete-key-modal').style.display='none'">取消</button>` |
| 213 | `        <button type="submit" class="btn btn-danger">确认删除</button>` |
| 242 | `      showToast('error', res.error.message \|\| '获取密钥失败');` |
| 248 | `  .catch(function() { showToast('error', '请求失败'); });` |
| 284 | `  .catch(function() { showToast('error', '请求失败'); });` |
| 290 | `    showToast('success', '已复制到剪贴板');` |
| 297 | `    showToast('success', '已复制到剪贴板');` |

## enterprise/usage.html (30 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">用量明细</h1>` |
| 6 | `      <a href="/api/enterprise/usage/export?dimension={{.Data.Dimension}}&from={{.Data.From}}&to={{.Data.To}}" class="btn btn-secondary">导出 CSV</a>` |
| 13 | `      <label class="form-label text-xs">维度</label>` |
| 15 | `        <option value="user" {{if eq .Data.Dimension "user"}}selected{{end}}>按用户</option>` |
| 16 | `        <option value="model" {{if eq .Data.Dimension "model"}}selected{{end}}>按模型</option>` |
| 17 | `        <option value="key" {{if eq .Data.Dimension "key"}}selected{{end}}>按密钥</option>` |
| 21 | `      <label class="form-label text-xs">开始日期</label>` |
| 25 | `      <label class="form-label text-xs">结束日期</label>` |
| 28 | `    <button class="btn btn-primary" onclick="reloadUsage()">查询</button>` |
| 29 | `    <button class="btn btn-secondary" onclick="resetDates()">本月</button>` |
| 36 | `    <div class="stat-label">输入 Token</div>` |
| 40 | `    <div class="stat-label">输入缓存命中</div>` |
| 44 | `    <div class="stat-label">输出 Token</div>` |
| 48 | `    <div class="stat-label">总 Token 数</div>` |
| 52 | `    <div class="stat-label">总请求数</div>` |
| 56 | `    <div class="stat-label">总费用 (微美元)</div>` |
| 64 | `    <h2 class="card-title" id="table-title">按用户</h2>` |
| 70 | `          <th id="col-name">用户</th>` |
| 71 | `          <th>请求数</th>` |
| 72 | `          <th>输入 Token</th>` |
| 73 | `          <th>缓存命中</th>` |
| 74 | `          <th>输出 Token</th>` |
| 75 | `          <th>总 Token</th>` |
| 76 | `          <th>费用 (微美元)</th>` |
| 77 | `          <th>占比</th>` |
| 81 | `        <tr><td colspan="8" class="text-center text-muted">加载中...</td></tr>` |
| 109 | `  var labels = {user: '用户', model: '模型', key: '密钥'};` |
| 110 | `  document.getElementById('table-title').textContent = '按' + labels[dim];` |
| 134 | `        tbody.innerHTML = '<tr><td colspan="8" class="text-center text-muted">暂无数据</td></tr>';` |
| 153 | `    .catch(function(e) { showToast('error', '加载失败: ' + e.message); });` |

## enterprise/users.html (46 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">企业成员管理</h1>` |
| 6 | `      <input type="text" id="user-search" class="form-input" placeholder="搜索邮箱" style="width: 200px" onkeyup="filterUsers()">` |
| 7 | `      <button class="btn btn-primary" onclick="document.getElementById('add-user-modal').style.display='flex'">添加成员</button>` |
| 14 | `    <span class="batch-toolbar-count"><span id="batch-count">0</span> 项已选择</span>` |
| 15 | `    <button class="btn btn-sm btn-danger" onclick="batchRemove()">批量移除</button>` |
| 16 | `    <button class="btn btn-sm btn-secondary" onclick="clearBatchSelection()">取消选择</button>` |
| 24 | `          <th>成员</th>` |
| 25 | `          <th>角色</th>` |
| 26 | `          <th>配额上限</th>` |
| 27 | `          <th>密钥数</th>` |
| 28 | `          <th>状态</th>` |
| 29 | `          <th>操作</th>` |
| 42 | `                <div class="font-medium">{{if .Name}}{{.Name}}{{else}}未设置{{end}}</div>` |
| 49 | `              {{if or (eq .Role "enterprise_admin") (eq .Role "admin")}}管理员{{else}}成员{{end}}` |
| 53 | `            {{if .QuotaLimit}}{{fmtNum .QuotaLimit}}{{else}}<span class="text-muted">无限制</span>{{end}}` |
| 58 | `              {{if eq .Status "active"}}正常{{else}}暂停{{end}}` |
| 63 | `              <button class="btn btn-sm btn-secondary" onclick="editUser('{{.ID}}', '{{.Name}}', '{{.Email}}', '{{.Role}}', {{.QuotaLimit}})">编辑</butt` |
| 65 | `              <button class="btn btn-sm btn-danger" onclick="confirmRemove('{{.ID}}', '{{.Name}}', '{{$.CSRFToken}}')">移除</button>` |
| 75 | `  <p class="text-muted text-center mt-4">暂无成员</p>` |
| 83 | `      <span class="modal-title">添加成员</span>` |
| 90 | `          <label class="form-label">邮箱 *</label>` |
| 94 | `          <label class="form-label">角色</label>` |
| 96 | `            <option value="member">成员</option>` |
| 97 | `            <option value="admin">管理员</option>` |
| 102 | `        <button type="button" class="btn btn-secondary" onclick="document.getElementById('add-user-modal').style.display='none'">取消</button>` |
| 103 | `        <button type="submit" class="btn btn-primary">添加</button>` |
| 113 | `      <span class="modal-title">编辑成员</span>` |
| 122 | `        <label class="form-label">角色</label>` |
| 124 | `          <option value="member">成员</option>` |
| 125 | `          <option value="admin">管理员</option>` |
| 129 | `        <label class="form-label">状态</label>` |
| 131 | `          <option value="active">正常</option>` |
| 132 | `          <option value="suspended">暂停</option>` |
| 136 | `        <label class="form-label">Token配额上限（0=无限制）</label>` |
| 137 | `        <input type="number" id="edit-user-quota-limit" class="form-input" min="0" step="1" placeholder="0 表示无限制">` |
| 141 | `      <button class="btn btn-secondary" onclick="document.getElementById('edit-user-modal').style.display='none'">取消</button>` |
| 142 | `      <button class="btn btn-primary" onclick="submitEditUser()">保存</button>` |
| 151 | `      <span class="modal-title">移除成员</span>` |
| 155 | `      <p>确定要移除成员 <strong id="remove-user-name"></strong> 吗？移除后将回收其企业密钥。</p>` |
| 158 | `      <button class="btn btn-secondary" onclick="document.getElementById('remove-user-modal').style.display='none'">取消</button>` |
| 161 | `        <button type="submit" class="btn btn-danger">确认移除</button>` |
| 207 | `      showToast('error', res.error.message \|\| '操作失败');` |
| 223 | `      showToast('error', res.error.message \|\| '操作失败');` |
| 228 | `  .catch(function() { showToast('error', '请求失败'); });` |
| 265 | `  if (!confirm('确定要移除选中的 ' + ids.length + ' 位成员吗？')) return;` |
| 267 | `  showToast('info', '批量移除 ' + ids.length + ' 位成员的功能即将上线');` |

## partials/audit-log-detail.html (7 处)

| 行号 | 代码 |
|------|------|
| 11 | `    <span class="text-muted">时间:</span>` |
| 15 | `    <span class="text-muted">用户:</span>` |
| 19 | `    <span class="text-muted">分类:</span>` |
| 23 | `    <span class="text-muted">操作:</span>` |
| 27 | `    <span class="text-muted">级别:</span>` |
| 31 | `    <span class="text-muted">消息:</span>` |
| 38 | `    <span class="text-muted">详细信息:</span>` |

## partials/key-created.html (8 处)

| 行号 | 代码 |
|------|------|
| 2 | `<div class="key-warning">⚠️ 此密钥仅显示一次，请立即复制保存。关闭后将无法再次查看完整密钥。</div>` |
| 7 | `    <button class="btn btn-sm btn-primary" onclick="copyToClipboard('new-api-key', this)">复制</button>` |
| 11 | `  <button class="btn btn-secondary" onclick="window.location.reload()">关闭</button>` |
| 20 | `    btn.textContent = '已复制';` |
| 21 | `    setTimeout(function() { btn.textContent = '复制'; }, 2000);` |
| 24 | `      btn.textContent = '已复制';` |
| 25 | `      setTimeout(function() { btn.textContent = '复制'; }, 2000);` |
| 27 | `      showMessage('复制失败', '请手动复制: ' + input.value);` |

## partials/order-created.html (13 处)

| 行号 | 代码 |
|------|------|
| 3 | `  <div class="text-lg font-bold text-success mb-2">✓ 订单已创建</div>` |
| 4 | `  <p class="text-muted">请保存订单号以便查询</p>` |
| 7 | `  <label class="form-label">订单号</label>` |
| 10 | `    <button class="btn btn-sm btn-primary" onclick="copyToClipboard('new-order-id', this)">复制</button>` |
| 14 | `  <label class="form-label">金额</label>` |
| 18 | `  <label class="form-label">状态</label>` |
| 22 | `  <a href="/orders" class="btn btn-primary">查看订单</a>` |
| 23 | `  <button class="btn btn-secondary" onclick="window.location.reload()">关闭</button>` |
| 32 | `    btn.textContent = '已复制';` |
| 33 | `    setTimeout(function() { btn.textContent = '复制'; }, 2000);` |
| 36 | `      btn.textContent = '已复制';` |
| 37 | `      setTimeout(function() { btn.textContent = '复制'; }, 2000);` |
| 39 | `      showMessage('复制失败', '请手动复制: ' + input.value);` |

## partials/plan-detail.html (21 处)

| 行号 | 代码 |
|------|------|
| 14 | `      <label class="form-label">名称</label>` |
| 18 | `      <label class="form-label">类型</label>` |
| 25 | `      <label class="form-label">价格 ($)</label>` |
| 29 | `      <label class="form-label">配额</label>` |
| 31 | `      <p class="text-xs text-muted mt-1">Token Plan: Token数量; Code Plan: 使用次数</p>` |
| 34 | `      <label class="form-label">有效期 (天)</label>` |
| 36 | `      <p class="text-xs text-muted mt-1">0 表示永久有效。此为用户购买后额度的使用期限（如 30 天内可用），非购买窗口期</p>` |
| 39 | `      <label class="form-label">每日限额</label>` |
| 41 | `      <p class="text-xs text-muted mt-1">0 表示无限制。Token Plan: 每日Token上限; Code Plan: 每日请求次数上限</p>` |
| 44 | `      <label class="form-label">速率限制</label>` |
| 47 | `          <span class="text-xs text-muted">RPH (请求/时)</span>` |
| 51 | `          <span class="text-xs text-muted">RP5H (请求/5小时)</span>` |
| 55 | `          <span class="text-xs text-muted">RPW (请求/周)</span>` |
| 59 | `          <span class="text-xs text-muted">TPH (Token/时)</span>` |
| 63 | `          <span class="text-xs text-muted">TPD (Token/日)</span>` |
| 67 | `          <span class="text-xs text-muted">TPW (Token/周)</span>` |
| 71 | `          <span class="text-xs text-muted">并发数</span>` |
| 75 | `      <p class="text-xs text-muted mt-1">0 表示无限制。Code Plan 使用 RPH/RP5H/RPW; Token Plan 使用 TPH/TPD/TPW</p>` |
| 84 | `      <span class="text-muted">状态:</span>` |
| 85 | `      <span class="badge badge-{{if eq .Status "active"}}success{{else}}error{{end}}">{{if eq .Status "active"}}活跃{{else}}禁用{{end}}</span>` |
| 89 | `    <button type="submit" class="btn btn-primary flex-1">保存修改</button>` |

## partials/recent-keys.html (7 处)

| 行号 | 代码 |
|------|------|
| 7 | `        <th>名称</th>` |
| 8 | `        <th>前缀</th>` |
| 9 | `        <th>状态</th>` |
| 10 | `        <th>最后使用</th>` |
| 19 | `          {{if eq .Status "active"}}<span class="badge badge-success">活跃</span>` |
| 20 | `          {{else}}<span class="badge badge-error">禁用</span>{{end}}` |
| 29 | `<div class="text-center py-4 text-muted">暂无 API Key，<a href="/apikeys" class="text-primary">立即创建</a></div>` |

## partials/user-detail.html (22 处)

| 行号 | 代码 |
|------|------|
| 6 | `    <label class="form-label">名称</label>` |
| 11 | `    <label class="form-label">邮箱</label>` |
| 13 | `    <span class="form-hint">邮箱不可修改</span>` |
| 18 | `      <label class="form-label">用户组</label>` |
| 20 | `        <option value="">— 无 —</option>` |
| 25 | `      <span class="form-hint">修改后只影响新创建的 API Key</span>` |
| 28 | `      <label class="form-label">用户类型</label>` |
| 30 | `        <option value="general" {{if eq .UserType "general"}}selected{{end}}>标准</option>` |
| 31 | `        <option value="economy" {{if eq .UserType "economy"}}selected{{end}}>折扣</option>` |
| 32 | `        <option value="subscription" {{if eq .UserType "subscription"}}selected{{end}}>套餐</option>` |
| 33 | `        <option value="enterprise" {{if eq .UserType "enterprise"}}selected{{end}}>企业</option>` |
| 39 | `    <label class="form-label">余额 (分)</label>` |
| 42 | `      <button type="button" class="btn btn-warning btn-sm" id="balance-edit-btn" onclick="toggleBalanceEdit()">修改余额</button>` |
| 44 | `    <span class="form-hint" id="balance-hint" style="color:var(--color-danger);display:none">⚠ 修改余额是危险操作，保存后不可撤销</span>` |
| 48 | `    <label class="form-label">角色</label>` |
| 56 | `    <span class="form-hint">仅允许修改为 user 或 enterprise_admin</span>` |
| 60 | `    <button type="button" class="btn btn-secondary" onclick="document.getElementById('user-modal').style.display='none'">取消</button>` |
| 61 | `    <button type="submit" class="btn btn-primary">保存</button>` |
| 68 | `      <span class="modal-title">确认修改角色</span>` |
| 71 | `      <p id="role-confirm-msg">确定要修改用户角色吗？</p>` |
| 73 | `        <button type="button" class="btn btn-secondary" onclick="document.getElementById('role-confirm-modal').style.display='none'">取消</button>` |
| 74 | `        <button type="button" class="btn btn-primary" id="role-confirm-btn">确认</button>` |

## billing/checkout.html (2 处)

| 行号 | 代码 |
|------|------|
| 30 | `      <span class="text-lg">支付宝</span>` |
| 34 | `      <span class="text-lg">微信支付</span>` |

## billing/checkout.html (2 处)

| 行号 | 代码 |
|------|------|
| 30 | `      <span class="text-lg">支付宝</span>` |
| 34 | `      <span class="text-lg">微信支付</span>` |

## enterprise/audit.html (24 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">审计日志</h1>` |
| 6 | `      <a href="/api/enterprise/audit/export?type=roles" class="btn btn-secondary btn-sm">导出角色变更</a>` |
| 7 | `      <a href="/api/enterprise/audit/export?type=credits" class="btn btn-secondary btn-sm">导出配额变更</a>` |
| 10 | `  <p class="text-muted">企业成员角色变更、配额/信用变更记录。</p>` |
| 16 | `    <h2 class="card-title">角色变更记录</h2>` |
| 23 | `          <th>时间</th>` |
| 24 | `          <th>成员</th>` |
| 25 | `          <th>变更</th>` |
| 26 | `          <th>操作人</th>` |
| 27 | `          <th>原因</th>` |
| 43 | `            {{if .changed_by_email}}{{.changed_by_email}}{{else}}系统{{end}}` |
| 55 | `    <p class="text-muted">暂无角色变更记录</p>` |
| 63 | `    <h2 class="card-title">配额/信用变更记录</h2>` |
| 70 | `          <th>时间</th>` |
| 71 | `          <th>类型</th>` |
| 72 | `          <th>字段</th>` |
| 73 | `          <th>变动</th>` |
| 74 | `          <th>操作人</th>` |
| 75 | `          <th>原因</th>` |
| 84 | `              {{if eq .change_type "quota_topup"}}配额充值` |
| 85 | `              {{else if eq .change_type "credit_topup"}}信用充值` |
| 86 | `              {{else if eq .change_type "monthly_reset"}}月度重置` |
| 87 | `              {{else if eq .change_type "admin_override"}}管理员操作` |
| 106 | `    <p class="text-muted">暂无配额/信用变更记录</p>` |

## enterprise/billing.html (17 处)

| 行号 | 代码 |
|------|------|
| 5 | `    <h1 class="card-title">企业账单</h1>` |
| 28 | `    <h2 class="card-title">信用额度概览</h2>` |
| 32 | `      <span class="credit-gauge-label">已使用 {{printf "%.1f" $d.CreditPercent}}%</span>` |
| 40 | `        <div class="stat-label">总额度</div>` |
| 44 | `        <div class="stat-label">已消费</div>` |
| 48 | `        <div class="stat-label">剩余</div>` |
| 52 | `        <div class="stat-label">使用率</div>` |
| 96 | `    <h2 class="card-title">按模型用量</h2>` |
| 102 | `          <th>模型</th>` |
| 103 | `          <th>Token 数</th>` |
| 104 | `          <th>请求数</th>` |
| 105 | `          <th>占比</th>` |
| 127 | `    <h2 class="card-title">定价信息</h2>` |
| 131 | `      <div class="stat-label">企业定价倍率</div>` |
| 133 | `      <div class="text-xs text-muted mt-1">{{if lt $d.PricingRatio 1.0}}优惠 {{mul (sub 1.0 $d.PricingRatio) 100}}%{{else if gt $d.PricingRatio 1.0}}溢价 ` |
| 136 | `      <div class="stat-label">计费周期</div>` |
| 141 | `      <div class="stat-label">下次重置</div>` |

## enterprise/dashboard.html (38 处)

| 行号 | 代码 |
|------|------|
| 50 | `        {{if eq $d.Status "suspended"}}企业已暂停{{else if eq $d.Status "admin_vacant"}}管理员空缺{{else}}{{$d.Status}}{{end}}` |
| 53 | `        {{if eq $d.Status "suspended"}}企业运营已暂停，请联系平台管理员恢复。现有密钥仍可使用，但无法进行管理操作。` |
| 54 | `        {{else if eq $d.Status "admin_vacant"}}当前无企业管理员，请联系平台管理员指派。成员可正常使用现有密钥，但无法进行管理操作。{{end}}` |
| 71 | `        <span>成员管理</span>` |
| 75 | `        <span>企业分组</span>` |
| 79 | `        <span>密钥管理</span>` |
| 83 | `        <span>企业账单</span>` |
| 87 | `        <span>用量明细</span>` |
| 91 | `        <span>审计日志</span>` |
| 103 | `    <div class="trend trend-neutral">上限 {{$d.MaxMembers}}</div>` |
| 129 | `    <h2 class="card-title">信用额度</h2>` |
| 131 | `    <span class="text-xs text-muted">下次重置：{{formatDateTime $d.NextResetAt $.Timezone}}</span>` |
| 137 | `      <span class="credit-gauge-label">已使用 {{printf "%.1f" $d.CreditPercent}}%</span>` |
| 145 | `        <div class="stat-label">总额度</div>` |
| 149 | `        <div class="stat-label">已消费</div>` |
| 153 | `        <div class="stat-label">剩余</div>` |
| 159 | `  <p class="text-muted text-center" style="padding:1rem">未配置信用额度</p>` |
| 167 | `    <h2 class="card-title">近 30 天用量趋势</h2>` |
| 168 | `    <a href="/enterprise/usage" class="text-xs text-muted">查看详情 →</a>` |
| 179 | `      <div style="flex:1;display:flex;flex-direction:column;align-items:center;gap:2px" title="{{.Date}}: {{fmtNum .Requests}} 次, {{fmtNum .Tokens}} t` |
| 196 | `    <h2 class="card-title">告警历史</h2>` |
| 202 | `          <th>时间</th>` |
| 203 | `          <th>类型</th>` |
| 204 | `          <th>阈值</th>` |
| 205 | `          <th>当前值</th>` |
| 206 | `          <th>上限值</th>` |
| 207 | `          <th>状态</th>` |
| 216 | `              {{if eq .AlertType "quota_warning"}}配额告警` |
| 217 | `              {{else if eq .AlertType "quota_exceeded"}}配额超限` |
| 218 | `              {{else if eq .AlertType "credit_warning"}}信用告警` |
| 219 | `              {{else if eq .AlertType "credit_exceeded"}}信用超限` |
| 227 | `            {{if .AcknowledgedAt}}<span class="badge badge-success">已确认</span>` |
| 228 | `            {{else}}<span class="badge badge-warning">待处理</span>{{end}}` |
| 241 | `  <h2 class="card-title">本月用量TOP用户</h2>` |
| 246 | `          <th>用户</th>` |
| 247 | `          <th>Token数</th>` |
| 248 | `          <th>请求数</th>` |
| 249 | `          <th>占比</th>` |

## enterprise/groups.html (57 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">企业分组管理</h1>` |
| 5 | `    <button class="btn btn-primary" onclick="openCreateModal()">新建分组</button>` |
| 13 | `          <th>名称</th>` |
| 14 | `          <th>基于平台分组</th>` |
| 15 | `          <th>可见性</th>` |
| 16 | `          <th>倍率</th>` |
| 17 | `          <th>模型</th>` |
| 19 | `          <th>操作</th>` |
| 35 | `            <span class="badge badge-error">管理员专用</span>` |
| 37 | `            <span class="badge badge-success">公开</span>` |
| 43 | `            <span class="text-sm" title="企业折扣 {{printf "%.2f" .EnterpriseRatio}}x × 平台倍率 {{printf "%.2f" .ParentGroupRatio}}x = 实际倍率">` |
| 64 | `              <button class="btn btn-sm btn-secondary" onclick="openEditModal('{{.ID}}', '{{.Name}}', '{{.Models}}', {{.RPM}}, {{.TPM}})">编辑</button>` |
| 65 | `              <button class="btn btn-sm btn-danger" onclick='confirmDeleteGroup("{{.ID}}", "{{.Name}}")'>删除</button>` |
| 74 | `  <p class="text-muted text-center mt-4">暂无分组。创建一个分组以限制成员的模型访问和调用频率。</p>` |
| 82 | `      <span class="modal-title" id="group-modal-title">新建分组</span>` |
| 88 | `        <label class="form-label">名称 *</label>` |
| 89 | `        <input type="text" id="group-name" class="form-input" placeholder="例如：研发团队-生产环境" required>` |
| 92 | `        <label class="form-label">选择平台分组 *</label>` |
| 94 | `          <option value="">-- 选择平台企业分组 --</option>` |
| 99 | `        <label class="form-label">选择模型</label>` |
| 101 | `          <span class="text-muted text-sm">请先选择平台分组</span>` |
| 106 | `          <label class="form-label">RPM (每分钟请求数)</label>` |
| 112 | `        <label class="form-label">TPM (每分钟Token数)</label>` |
| 113 | `        <input type="number" id="group-tpm" class="form-input" value="0" min="0" placeholder="0 = 不限制">` |
| 114 | `        <span class="text-xs" style="color:#f59e0b;">⚠ 不建议随意配置。设为0即不限制。</span>` |
| 117 | `        <label class="form-label">分组可见性</label>` |
| 119 | `          <option value="public">公开 — 所有成员可见，成员可自行创建密钥使用</option>` |
| 120 | `          <option value="admin_only">管理员专用 — 仅管理员可分配，对成员隐藏</option>` |
| 125 | `      <button class="btn btn-secondary" onclick="closeGroupModal()">取消</button>` |
| 126 | `      <button class="btn btn-primary" id="group-save-btn" onclick="saveGroup()">创建</button>` |
| 135 | `      <span class="modal-title">删除分组</span>` |
| 139 | `      <p>确定要删除分组 <strong id="delete-group-name"></strong> 吗？</p>` |
| 142 | `      <button class="btn btn-secondary" onclick="document.getElementById('delete-group-modal').style.display='none'">取消</button>` |
| 143 | `      <button class="btn btn-danger" id="delete-group-btn">确认删除</button>` |
| 160 | `      select.innerHTML = '<option value="">-- 选择平台企业分组 --</option>';` |
| 162 | `        var modelsInfo = g.model_count > 0 ? g.model_count + ' 个模型' : '所有模型';` |
| 167 | `    .catch(function() { showToast('error', '加载平台分组失败'); });` |
| 176 | `    document.getElementById('model-checkboxes').innerHTML = '<span class="text-muted text-sm">请先选择平台分组</span>';` |
| 192 | `    if (g.rpmonth) limits.push('RP/月:' + g.rpmonth);` |
| 195 | `    if (g.tpmonth) limits.push('TP/月:' + g.tpmonth);` |
| 196 | `    detailEl.innerHTML = '平台分组详情 — 限速: ' + (limits.length ? limits.join(', ') : '无限制') +` |
| 197 | `      ' \| 倍率: ' + g.pricing_ratio + 'x \| 模型: ' + (g.model_count > 0 ? g.model_count + '个' : '全部');` |
| 202 | `  document.getElementById('model-checkboxes').innerHTML = '<span class="text-muted text-sm">加载模型列表...</span>';` |
| 209 | `    .catch(function() { showToast('error', '加载模型列表失败'); });` |
| 216 | `    container.innerHTML = '<span class="text-muted text-sm">该平台分组未限制模型（可选择所有模型）</span>';` |
| 241 | `  document.getElementById('group-modal-title').textContent = '新建分组';` |
| 242 | `  document.getElementById('group-save-btn').textContent = '创建';` |
| 243 | `  document.getElementById('model-checkboxes').innerHTML = '<span class="text-muted text-sm">请先选择平台分组</span>';` |
| 254 | `  document.getElementById('group-modal-title').textContent = '编辑分组';` |
| 255 | `  document.getElementById('group-save-btn').textContent = '保存';` |
| 275 | `  if (!name) { showToast('error', '请输入分组名称'); return; }` |
| 279 | `    showToast('error', '请选择平台分组'); return;` |
| 286 | `    showToast('error', 'RPM 不能超过平台分组的限制 (' + maxRPM + ')'); return;` |
| 313 | `    else { showToast('error', (res.error && res.error.message) \|\| '操作失败'); }` |
| 315 | `  .catch(function() { showToast('error', '请求失败'); });` |
| 331 | `        else { showToast('error', (res.error && res.error.message) \|\| '删除失败'); }` |
| 333 | `      .catch(function() { showToast('error', '请求失败'); });` |

## enterprise/keys.html (56 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">企业密钥管理</h1>` |
| 5 | `    <button class="btn btn-primary" onclick="document.getElementById('create-key-modal').style.display='flex'">创建新密钥</button>` |
| 13 | `          <th>名称</th>` |
| 14 | `          <th>密钥前缀</th>` |
| 15 | `          <th>分配给</th>` |
| 16 | `          <th>分组</th>` |
| 17 | `          <th>状态</th>` |
| 18 | `          <th>最近使用</th>` |
| 19 | `          <th>创建时间</th>` |
| 20 | `          <th>操作</th>` |
| 28 | `          <td>{{if .AssignedUserName}}{{.AssignedUserName}}{{else}}<span class="text-muted">未分配</span>{{end}}</td>` |
| 32 | `              {{if eq .Status "active"}}正常{{else}}已禁用{{end}}` |
| 36 | `            <span class="text-muted">从未使用</span>` |
| 41 | `              <button class="btn btn-sm btn-secondary" onclick="viewKey('{{.ID}}')">查看完整密钥</button>` |
| 43 | `              <button class="btn btn-sm btn-secondary" onclick="editKey('{{.ID}}', '{{.Name}}', '{{.AssignedUserID}}', '{{.GroupID}}')">编辑</button>` |
| 44 | `              <button class="btn btn-sm btn-danger" onclick="confirmRevoke('{{.ID}}', '{{.Name}}')">禁用</button>` |
| 46 | `              <button class="btn btn-sm btn-success" onclick="reactivateKey('{{.ID}}')">激活</button>` |
| 48 | `              <button class="btn btn-sm btn-danger" onclick="confirmDelete('{{.ID}}', '{{.Name}}')">删除</button>` |
| 57 | `  <p class="text-muted text-center mt-4">暂无密钥</p>` |
| 65 | `      <span class="modal-title">创建新密钥</span>` |
| 72 | `          <label class="form-label">密钥名称 *</label>` |
| 73 | `          <input type="text" name="name" class="form-input" required placeholder="例如：生产环境密钥">` |
| 76 | `          <label class="form-label">分配给成员</label>` |
| 78 | `            <option value="">不指定</option>` |
| 85 | `          <label class="form-label">选择企业分组</label>` |
| 87 | `            <option value="">不指定（使用默认方式）</option>` |
| 92 | `          <span class="text-xs text-muted mt-1">选择企业分组可限制该成员可用的模型和调用频率。</span>` |
| 96 | `        <button type="button" class="btn btn-secondary" onclick="document.getElementById('create-key-modal').style.display='none'">取消</button>` |
| 97 | `        <button type="submit" class="btn btn-primary">创建</button>` |
| 107 | `      <span class="modal-title">密钥详情</span>` |
| 111 | `      <div class="key-warning">请妥善保存以下密钥，关闭后无法再次查看完整密钥。</div>` |
| 114 | `        <button class="btn btn-sm btn-secondary" onclick="copyKey()">复制</button>` |
| 118 | `      <button class="btn btn-secondary" onclick="document.getElementById('view-key-modal').style.display='none'">关闭</button>` |
| 127 | `      <span class="modal-title">密钥创建成功</span>` |
| 131 | `      <div class="key-warning">请立即保存以下密钥，此为唯一一次查看完整密钥的机会。</div>` |
| 134 | `        <button class="btn btn-sm btn-secondary" onclick="copyNewKey()">复制</button>` |
| 138 | `      <button class="btn btn-primary" onclick="document.getElementById('new-key-modal').style.display='none'">我已保存</button>` |
| 147 | `      <span class="modal-title">编辑密钥</span>` |
| 155 | `        <label class="form-label">重新分配给</label>` |
| 163 | `        <label class="form-label">所属分组</label>` |
| 165 | `          <option value="">不指定</option>` |
| 173 | `      <button class="btn btn-secondary" onclick="document.getElementById('edit-key-modal').style.display='none'">取消</button>` |
| 174 | `      <button class="btn btn-primary" onclick="submitEditKey()">保存</button>` |
| 183 | `      <span class="modal-title">禁用密钥</span>` |
| 187 | `      <p>确定要禁用密钥 <strong id="revoke-key-name"></strong> 吗？禁用后使用该密钥的请求将被拒绝。</p>` |
| 190 | `      <button class="btn btn-secondary" onclick="document.getElementById('revoke-key-modal').style.display='none'">取消</button>` |
| 193 | `        <button type="submit" class="btn btn-danger">确认禁用</button>` |
| 203 | `      <span class="modal-title">删除密钥</span>` |
| 207 | `      <p>确定要删除密钥 <strong id="delete-key-name"></strong> 吗？此操作不可恢复。</p>` |
| 210 | `      <button class="btn btn-secondary" onclick="document.getElementById('delete-key-modal').style.display='none'">取消</button>` |
| 213 | `        <button type="submit" class="btn btn-danger">确认删除</button>` |
| 242 | `      showToast('error', res.error.message \|\| '获取密钥失败');` |
| 248 | `  .catch(function() { showToast('error', '请求失败'); });` |
| 284 | `  .catch(function() { showToast('error', '请求失败'); });` |
| 290 | `    showToast('success', '已复制到剪贴板');` |
| 297 | `    showToast('success', '已复制到剪贴板');` |

## enterprise/usage.html (30 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">用量明细</h1>` |
| 6 | `      <a href="/api/enterprise/usage/export?dimension={{.Data.Dimension}}&from={{.Data.From}}&to={{.Data.To}}" class="btn btn-secondary">导出 CSV</a>` |
| 13 | `      <label class="form-label text-xs">维度</label>` |
| 15 | `        <option value="user" {{if eq .Data.Dimension "user"}}selected{{end}}>按用户</option>` |
| 16 | `        <option value="model" {{if eq .Data.Dimension "model"}}selected{{end}}>按模型</option>` |
| 17 | `        <option value="key" {{if eq .Data.Dimension "key"}}selected{{end}}>按密钥</option>` |
| 21 | `      <label class="form-label text-xs">开始日期</label>` |
| 25 | `      <label class="form-label text-xs">结束日期</label>` |
| 28 | `    <button class="btn btn-primary" onclick="reloadUsage()">查询</button>` |
| 29 | `    <button class="btn btn-secondary" onclick="resetDates()">本月</button>` |
| 36 | `    <div class="stat-label">输入 Token</div>` |
| 40 | `    <div class="stat-label">输入缓存命中</div>` |
| 44 | `    <div class="stat-label">输出 Token</div>` |
| 48 | `    <div class="stat-label">总 Token 数</div>` |
| 52 | `    <div class="stat-label">总请求数</div>` |
| 56 | `    <div class="stat-label">总费用 (微美元)</div>` |
| 64 | `    <h2 class="card-title" id="table-title">按用户</h2>` |
| 70 | `          <th id="col-name">用户</th>` |
| 71 | `          <th>请求数</th>` |
| 72 | `          <th>输入 Token</th>` |
| 73 | `          <th>缓存命中</th>` |
| 74 | `          <th>输出 Token</th>` |
| 75 | `          <th>总 Token</th>` |
| 76 | `          <th>费用 (微美元)</th>` |
| 77 | `          <th>占比</th>` |
| 81 | `        <tr><td colspan="8" class="text-center text-muted">加载中...</td></tr>` |
| 109 | `  var labels = {user: '用户', model: '模型', key: '密钥'};` |
| 110 | `  document.getElementById('table-title').textContent = '按' + labels[dim];` |
| 134 | `        tbody.innerHTML = '<tr><td colspan="8" class="text-center text-muted">暂无数据</td></tr>';` |
| 153 | `    .catch(function(e) { showToast('error', '加载失败: ' + e.message); });` |

## enterprise/users.html (46 处)

| 行号 | 代码 |
|------|------|
| 4 | `    <h1 class="card-title">企业成员管理</h1>` |
| 6 | `      <input type="text" id="user-search" class="form-input" placeholder="搜索邮箱" style="width: 200px" onkeyup="filterUsers()">` |
| 7 | `      <button class="btn btn-primary" onclick="document.getElementById('add-user-modal').style.display='flex'">添加成员</button>` |
| 14 | `    <span class="batch-toolbar-count"><span id="batch-count">0</span> 项已选择</span>` |
| 15 | `    <button class="btn btn-sm btn-danger" onclick="batchRemove()">批量移除</button>` |
| 16 | `    <button class="btn btn-sm btn-secondary" onclick="clearBatchSelection()">取消选择</button>` |
| 24 | `          <th>成员</th>` |
| 25 | `          <th>角色</th>` |
| 26 | `          <th>配额上限</th>` |
| 27 | `          <th>密钥数</th>` |
| 28 | `          <th>状态</th>` |
| 29 | `          <th>操作</th>` |
| 42 | `                <div class="font-medium">{{if .Name}}{{.Name}}{{else}}未设置{{end}}</div>` |
| 49 | `              {{if or (eq .Role "enterprise_admin") (eq .Role "admin")}}管理员{{else}}成员{{end}}` |
| 53 | `            {{if .QuotaLimit}}{{fmtNum .QuotaLimit}}{{else}}<span class="text-muted">无限制</span>{{end}}` |
| 58 | `              {{if eq .Status "active"}}正常{{else}}暂停{{end}}` |
| 63 | `              <button class="btn btn-sm btn-secondary" onclick="editUser('{{.ID}}', '{{.Name}}', '{{.Email}}', '{{.Role}}', {{.QuotaLimit}})">编辑</butt` |
| 65 | `              <button class="btn btn-sm btn-danger" onclick="confirmRemove('{{.ID}}', '{{.Name}}', '{{$.CSRFToken}}')">移除</button>` |
| 75 | `  <p class="text-muted text-center mt-4">暂无成员</p>` |
| 83 | `      <span class="modal-title">添加成员</span>` |
| 90 | `          <label class="form-label">邮箱 *</label>` |
| 94 | `          <label class="form-label">角色</label>` |
| 96 | `            <option value="member">成员</option>` |
| 97 | `            <option value="admin">管理员</option>` |
| 102 | `        <button type="button" class="btn btn-secondary" onclick="document.getElementById('add-user-modal').style.display='none'">取消</button>` |
| 103 | `        <button type="submit" class="btn btn-primary">添加</button>` |
| 113 | `      <span class="modal-title">编辑成员</span>` |
| 122 | `        <label class="form-label">角色</label>` |
| 124 | `          <option value="member">成员</option>` |
| 125 | `          <option value="admin">管理员</option>` |
| 129 | `        <label class="form-label">状态</label>` |
| 131 | `          <option value="active">正常</option>` |
| 132 | `          <option value="suspended">暂停</option>` |
| 136 | `        <label class="form-label">Token配额上限（0=无限制）</label>` |
| 137 | `        <input type="number" id="edit-user-quota-limit" class="form-input" min="0" step="1" placeholder="0 表示无限制">` |
| 141 | `      <button class="btn btn-secondary" onclick="document.getElementById('edit-user-modal').style.display='none'">取消</button>` |
| 142 | `      <button class="btn btn-primary" onclick="submitEditUser()">保存</button>` |
| 151 | `      <span class="modal-title">移除成员</span>` |
| 155 | `      <p>确定要移除成员 <strong id="remove-user-name"></strong> 吗？移除后将回收其企业密钥。</p>` |
| 158 | `      <button class="btn btn-secondary" onclick="document.getElementById('remove-user-modal').style.display='none'">取消</button>` |
| 161 | `        <button type="submit" class="btn btn-danger">确认移除</button>` |
| 207 | `      showToast('error', res.error.message \|\| '操作失败');` |
| 223 | `      showToast('error', res.error.message \|\| '操作失败');` |
| 228 | `  .catch(function() { showToast('error', '请求失败'); });` |
| 265 | `  if (!confirm('确定要移除选中的 ' + ids.length + ' 位成员吗？')) return;` |
| 267 | `  showToast('info', '批量移除 ' + ids.length + ' 位成员的功能即将上线');` |

## partials/audit-log-detail.html (7 处)

| 行号 | 代码 |
|------|------|
| 11 | `    <span class="text-muted">时间:</span>` |
| 15 | `    <span class="text-muted">用户:</span>` |
| 19 | `    <span class="text-muted">分类:</span>` |
| 23 | `    <span class="text-muted">操作:</span>` |
| 27 | `    <span class="text-muted">级别:</span>` |
| 31 | `    <span class="text-muted">消息:</span>` |
| 38 | `    <span class="text-muted">详细信息:</span>` |

## partials/key-created.html (8 处)

| 行号 | 代码 |
|------|------|
| 2 | `<div class="key-warning">⚠️ 此密钥仅显示一次，请立即复制保存。关闭后将无法再次查看完整密钥。</div>` |
| 7 | `    <button class="btn btn-sm btn-primary" onclick="copyToClipboard('new-api-key', this)">复制</button>` |
| 11 | `  <button class="btn btn-secondary" onclick="window.location.reload()">关闭</button>` |
| 20 | `    btn.textContent = '已复制';` |
| 21 | `    setTimeout(function() { btn.textContent = '复制'; }, 2000);` |
| 24 | `      btn.textContent = '已复制';` |
| 25 | `      setTimeout(function() { btn.textContent = '复制'; }, 2000);` |
| 27 | `      showMessage('复制失败', '请手动复制: ' + input.value);` |

## partials/order-created.html (13 处)

| 行号 | 代码 |
|------|------|
| 3 | `  <div class="text-lg font-bold text-success mb-2">✓ 订单已创建</div>` |
| 4 | `  <p class="text-muted">请保存订单号以便查询</p>` |
| 7 | `  <label class="form-label">订单号</label>` |
| 10 | `    <button class="btn btn-sm btn-primary" onclick="copyToClipboard('new-order-id', this)">复制</button>` |
| 14 | `  <label class="form-label">金额</label>` |
| 18 | `  <label class="form-label">状态</label>` |
| 22 | `  <a href="/orders" class="btn btn-primary">查看订单</a>` |
| 23 | `  <button class="btn btn-secondary" onclick="window.location.reload()">关闭</button>` |
| 32 | `    btn.textContent = '已复制';` |
| 33 | `    setTimeout(function() { btn.textContent = '复制'; }, 2000);` |
| 36 | `      btn.textContent = '已复制';` |
| 37 | `      setTimeout(function() { btn.textContent = '复制'; }, 2000);` |
| 39 | `      showMessage('复制失败', '请手动复制: ' + input.value);` |

## partials/plan-detail.html (21 处)

| 行号 | 代码 |
|------|------|
| 14 | `      <label class="form-label">名称</label>` |
| 18 | `      <label class="form-label">类型</label>` |
| 25 | `      <label class="form-label">价格 ($)</label>` |
| 29 | `      <label class="form-label">配额</label>` |
| 31 | `      <p class="text-xs text-muted mt-1">Token Plan: Token数量; Code Plan: 使用次数</p>` |
| 34 | `      <label class="form-label">有效期 (天)</label>` |
| 36 | `      <p class="text-xs text-muted mt-1">0 表示永久有效。此为用户购买后额度的使用期限（如 30 天内可用），非购买窗口期</p>` |
| 39 | `      <label class="form-label">每日限额</label>` |
| 41 | `      <p class="text-xs text-muted mt-1">0 表示无限制。Token Plan: 每日Token上限; Code Plan: 每日请求次数上限</p>` |
| 44 | `      <label class="form-label">速率限制</label>` |
| 47 | `          <span class="text-xs text-muted">RPH (请求/时)</span>` |
| 51 | `          <span class="text-xs text-muted">RP5H (请求/5小时)</span>` |
| 55 | `          <span class="text-xs text-muted">RPW (请求/周)</span>` |
| 59 | `          <span class="text-xs text-muted">TPH (Token/时)</span>` |
| 63 | `          <span class="text-xs text-muted">TPD (Token/日)</span>` |
| 67 | `          <span class="text-xs text-muted">TPW (Token/周)</span>` |
| 71 | `          <span class="text-xs text-muted">并发数</span>` |
| 75 | `      <p class="text-xs text-muted mt-1">0 表示无限制。Code Plan 使用 RPH/RP5H/RPW; Token Plan 使用 TPH/TPD/TPW</p>` |
| 84 | `      <span class="text-muted">状态:</span>` |
| 85 | `      <span class="badge badge-{{if eq .Status "active"}}success{{else}}error{{end}}">{{if eq .Status "active"}}活跃{{else}}禁用{{end}}</span>` |
| 89 | `    <button type="submit" class="btn btn-primary flex-1">保存修改</button>` |

## partials/recent-keys.html (7 处)

| 行号 | 代码 |
|------|------|
| 7 | `        <th>名称</th>` |
| 8 | `        <th>前缀</th>` |
| 9 | `        <th>状态</th>` |
| 10 | `        <th>最后使用</th>` |
| 19 | `          {{if eq .Status "active"}}<span class="badge badge-success">活跃</span>` |
| 20 | `          {{else}}<span class="badge badge-error">禁用</span>{{end}}` |
| 29 | `<div class="text-center py-4 text-muted">暂无 API Key，<a href="/apikeys" class="text-primary">立即创建</a></div>` |

## partials/user-detail.html (22 处)

| 行号 | 代码 |
|------|------|
| 6 | `    <label class="form-label">名称</label>` |
| 11 | `    <label class="form-label">邮箱</label>` |
| 13 | `    <span class="form-hint">邮箱不可修改</span>` |
| 18 | `      <label class="form-label">用户组</label>` |
| 20 | `        <option value="">— 无 —</option>` |
| 25 | `      <span class="form-hint">修改后只影响新创建的 API Key</span>` |
| 28 | `      <label class="form-label">用户类型</label>` |
| 30 | `        <option value="general" {{if eq .UserType "general"}}selected{{end}}>标准</option>` |
| 31 | `        <option value="economy" {{if eq .UserType "economy"}}selected{{end}}>折扣</option>` |
| 32 | `        <option value="subscription" {{if eq .UserType "subscription"}}selected{{end}}>套餐</option>` |
| 33 | `        <option value="enterprise" {{if eq .UserType "enterprise"}}selected{{end}}>企业</option>` |
| 39 | `    <label class="form-label">余额 (分)</label>` |
| 42 | `      <button type="button" class="btn btn-warning btn-sm" id="balance-edit-btn" onclick="toggleBalanceEdit()">修改余额</button>` |
| 44 | `    <span class="form-hint" id="balance-hint" style="color:var(--color-danger);display:none">⚠ 修改余额是危险操作，保存后不可撤销</span>` |
| 48 | `    <label class="form-label">角色</label>` |
| 56 | `    <span class="form-hint">仅允许修改为 user 或 enterprise_admin</span>` |
| 60 | `    <button type="button" class="btn btn-secondary" onclick="document.getElementById('user-modal').style.display='none'">取消</button>` |
| 61 | `    <button type="submit" class="btn btn-primary">保存</button>` |
| 68 | `      <span class="modal-title">确认修改角色</span>` |
| 71 | `      <p id="role-confirm-msg">确定要修改用户角色吗？</p>` |
| 73 | `        <button type="button" class="btn btn-secondary" onclick="document.getElementById('role-confirm-modal').style.display='none'">取消</button>` |
| 74 | `        <button type="button" class="btn btn-primary" id="role-confirm-btn">确认</button>` |

## super/payment.html (9 处)

| 行号 | 代码 |
|------|------|
| 11 | `      导出配置` |
| 18 | `  <h2 class="card-title">支付模式</h2>` |
| 21 | `      <div class="font-medium" id="payment-mode-label">当前模式：真实支付</div>` |
| 22 | `      <p class="text-xs text-muted mt-1">模拟充值模式下，用户充值将直接成功，不经过支付网关。适用于测试环境。</p>` |
| 58 | `    label.textContent = '当前模式：模拟充值（测试）';` |
| 62 | `    label.textContent = '当前模式：真实支付';` |
| 85 | `        status.textContent = '⚠ 已切换到模拟充值模式，用户充值将直接成功。';` |
| 89 | `        status.textContent = '✓ 已切换到真实支付模式，用户充值需要通过支付网关。';` |
| 96 | `    showToast('error', '设置保存失败');` |

## super/providers.html (23 处)

| 行号 | 代码 |
|------|------|
| 15 | `          <th>名称</th>` |
| 16 | `          <th>类型</th>` |
| 18 | `          <th>状态</th>` |
| 19 | `          <th>操作</th>` |
| 29 | `          <td><span class="badge badge-{{if .Enabled}}success{{else}}error{{end}}">{{if .Enabled}}活跃{{else}}禁用{{end}}</span></td>` |
| 32 | `              <button class="btn btn-sm btn-ghost" onclick="editProvider('{{.ID}}', '{{.Name}}', '{{.Type}}', '{{.BaseURL}}')" title="编辑">` |
| 35 | `              <button class="btn btn-sm btn-secondary" onclick="toggleProvider('{{.ID}}', {{if .Enabled}}false{{else}}true{{end}})">{{if .Enabled}}禁用{` |
| 36 | `              <button class="btn btn-sm btn-danger" onclick="confirmDelete('{{.ID}}', '{{.Name}}')">删除</button>` |
| 91 | `      <span class="modal-title">编辑供应商</span>` |
| 169 | `        showMessage('添加失败', data.error.message \|\| JSON.stringify(data.error));` |
| 171 | `        showMessage('添加成功', '供应商已成功添加', function() { window.location.reload(); });` |
| 176 | `      showMessage('添加失败', err.message);` |
| 196 | `        showMessage('编辑失败', data.error.message \|\| JSON.stringify(data.error));` |
| 198 | `        showMessage('编辑成功', '供应商信息已更新', function() { window.location.reload(); });` |
| 203 | `      showMessage('编辑失败', err.message);` |
| 208 | `  var action = enable ? '启用' : '禁用';` |
| 217 | `        showMessage('操作失败', data.error.message \|\| JSON.stringify(data.error));` |
| 219 | `        showMessage('操作成功', '供应商已' + action, function() { window.location.reload(); });` |
| 222 | `    .catch(err => showMessage('操作失败', err.message));` |
| 227 | `  showConfirm('删除供应商', '确定要删除供应商 "' + providerName + '" 吗？此操作不可撤销。', doDelete);` |
| 238 | `        showMessage('删除失败', data.error.message \|\| JSON.stringify(data.error));` |
| 240 | `        showMessage('删除成功', '供应商已删除', function() { window.location.reload(); });` |
| 243 | `    .catch(err => showMessage('删除失败', err.message))` |
