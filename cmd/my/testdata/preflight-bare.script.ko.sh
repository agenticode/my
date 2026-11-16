#!/bin/sh
# 호스트 web-07의 my preflight --script, 2026-11-16 03:12 UTC. my는 이 명령을 하나도 실행하지 않았음.
# 실행하기 전에 블록마다 읽어야 함. 커널 파라미터는 다음 부팅부터 적용됨.
set -e

# journal: /var/log/journal 디렉터리가 없고 Storage=도 설정되지 않음. 저널을 메모리에만 두므로 reset 뒤에 남지 않음
# 저널을 디스크에 저장 (Storage=persistent)
# 비용: 디스크의 저널 파일. 기본값은 파일 시스템의 10%, 최대 4G
# 근거: journald.conf(5): Storage=, SystemMaxUse=
# 되돌리기: rm /etc/systemd/journald.conf.d/90-my-persistent.conf
# 되돌리기: systemctl restart systemd-journald.service
mkdir -p /etc/systemd/journald.conf.d
printf '[Journal]\nStorage=persistent\n' > /etc/systemd/journald.conf.d/90-my-persistent.conf
systemctl restart systemd-journald.service

# pstore-backend: 등록된 백엔드 없음: 펌웨어에 ERST 테이블이 없음, efi_pstore의 pstore_disable 값이 Y임
# efi_pstore가 panic 내용을 EFI 변수에 저장하게 함
# 비용: panic마다 커널 로그의 마지막 10240바이트를 1024바이트 단위 EFI 변수로 펌웨어의 변수 저장소에 씀. systemd-pstore나 사람이 파일을 지울 때까지 남음
# 근거: drivers/firmware/efi/efi-pstore.c (record_size, pstore_disable); fs/pstore/Kconfig (PSTORE_DEFAULT_KMSG_BYTES)
# 되돌리기: echo Y > /sys/module/efi_pstore/parameters/pstore_disable
# 되돌리기: rm /etc/default/grub.d/90-my-efi-pstore.cfg
# 되돌리기: update-grub
# 다음 부팅부터 적용됨
echo N > /sys/module/efi_pstore/parameters/pstore_disable
mkdir -p /etc/default/grub.d
echo 'GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT efi_pstore.pstore_disable=0"' > /etc/default/grub.d/90-my-efi-pstore.cfg
update-grub

# pstore-archive: systemd-pstore.service가 활성화되지 않음. 파일이 /sys/fs/pstore에 남고, 저장소가 차면 이후의 panic은 저장되지 않음
# 부팅할 때마다 systemd-pstore가 덤프를 저장소 밖으로 옮기게 함
# 비용: panic마다 /var/lib/systemd/pstore 아래에 커널 로그 끝부분이 담긴 파일 몇 개
# 근거: systemd-pstore.service(8); pstore.conf(5)
# 되돌리기: systemctl disable systemd-pstore.service
systemctl enable systemd-pstore.service

# kdump: crash 커널이 로드되지 않았고 명령줄에 crashkernel=도 없음
# crash 커널용 메모리를 예약하고 배포판의 kdump 서비스를 활성화
# 비용: 패키지가 메모리 512 MB 이상인 호스트에서 crash 커널용으로 192 MB를 예약함 (crashkernel=512M-:192M)
# 근거: Debian kdump-tools: debian/kdump-tools.grub.default, debian/rules
# 되돌리기: apt purge kdump-tools
# 되돌리기: update-grub
# 다음 부팅부터 적용됨
apt install kdump-tools
update-grub

# mce-print: 뱅크에 남은 기록이 /dev/mcelog로 가고 출력되지 않으며, 실행 중인 mcelog 데몬도 없음
# 모든 머신 체크 기록을 커널 로그에 출력 (mce=print_all)
# 비용: CE를 포함한 모든 머신 체크 기록이 emergency 수준으로 출력됨
# 근거: arch/x86/kernel/cpu/mce/core.c (mce_default_notifier, __print_mce)
# 되돌리기: rm /etc/default/grub.d/90-my-print-all.cfg
# 되돌리기: update-grub
# 다음 부팅부터 적용됨
mkdir -p /etc/default/grub.d
echo 'GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT mce=print_all"' > /etc/default/grub.d/90-my-print-all.cfg
update-grub

# console: 호스트 밖으로 나가는 콘솔이 없음(명령줄에 console= 없음). reset 직전의 마지막 줄은 화면에만 남음
# 직렬 콘솔을 추가하고(console=tty0 console=ttyS0,115200) 포트 출력을 BMC의 직렬 로그나 콘솔 서버로 남기거나, netconsole로 커널 로그를 다른 호스트로 보냄
# 비용: netconsole은 부팅 초기의 panic을 잡지 못함(netconsole.rst). 직렬 콘솔에는 출력을 보관할 콘솔 서버나 포트를 기록하는 BMC가 필요함
# 근거: Documentation/admin-guide/serial-console.rst; Documentation/networking/netconsole.rst
# 이 호스트에 맞는 명령 없음. 직접 해야 함

# rasdaemon: rasdaemon이 설치되지 않음. 부팅 중의 CE는 그 부팅의 커널 로그에만 남음
# rasdaemon을 --record로 실행해 실행 중인 부팅의 오류를 reset 전에 데이터베이스에 남김
# 비용: 커널이 추적하는 모든 오류를 /var/lib/rasdaemon 아래 SQLite 데이터베이스에 저장하는 데몬
# 근거: rasdaemon(8): --record
# 되돌리기: systemctl disable --now rasdaemon.service
apt install rasdaemon
systemctl enable --now rasdaemon.service

# harvest: my-harvest.service가 설치되지 않음. 이전 부팅이 남긴 기록은 누군가 my를 실행할 때만 읽힘
# 부팅할 때마다 이전 부팅이 남긴 기록을 보관 원장에 읽어 넣음
# 비용: /etc/systemd/system/my-harvest.service를 쓰고 활성화함. 부팅할 때마다 한 번 실행해 호스트를 읽고 기록을 /var/lib/my/ledger.db에 더함
# 근거: docs/crash-loop.md
# 되돌리기: my harvest uninstall
my harvest install
