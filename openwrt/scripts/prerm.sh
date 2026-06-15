#!/bin/sh
# =============================================================================
# nfpm preremove — 卸载/升级前停止并禁用 cfstmgrd 服务
#   opkg 升级时会先对旧包跑 prerm、再对新包跑 postinst，故升级也会短暂重启。
#   镜像构建阶段 ($IPKG_INSTROOT 非空) 跳过。
# =============================================================================
[ -n "${IPKG_INSTROOT}" ] && exit 0

if [ -x /etc/init.d/cfstmgrd ]; then
	/etc/init.d/cfstmgrd stop    2>/dev/null
	/etc/init.d/cfstmgrd disable 2>/dev/null
fi

exit 0
