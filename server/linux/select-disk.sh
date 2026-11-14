# Read-only selection helper. Parameters make the policy testable using a fake
# sysfs tree; generated production guards always pass /sys/block /dev /proc.
is_block_disk() { [ -b "$1" ]; }
select_disk() {
    sysroot=$1 devroot=$2 procroot=$3 min_gb=$4
    [ -r "$procroot/mounts" ] && [ -r "$procroot/swaps" ] || return 1
    # Prefer ordinary disks over MMC. Exclude virtual aggregate, optical and
    # network block devices; USB ancestors, removable media, mounted disks,
    # active swap and disks with holders must never qualify.
    for group in ordinary mmc; do
        for sys in "$sysroot"/*; do
            name=${sys##*/}
            case "$group:$name" in
                ordinary:sd*|ordinary:hd*|ordinary:vd*|ordinary:xvd*|ordinary:nvme*n*) ;;
                mmc:mmcblk*) ;;
                *) continue ;;
            esac
            is_block_disk "$devroot/$name" || continue
            [ "$(cat "$sys/removable")" = 0 ] || continue
            device_path=$(readlink -f "$sys") || continue
            case "$device_path" in */usb*/*|*/session[0-9]*/*|*/rport-*/*) continue ;; esac
            [ "$(cat "$sys/size")" -ge "$((min_gb * 2097152))" ] || continue
            busy=false
            # A mounted dm/LVM/crypt/md volume may hold a partition rather
            # than the whole disk. Exclude either form before partitioning.
            for holder in "$sys"/holders/* "$sys"/*/holders/*; do
                [ ! -e "$holder" ] || busy=true
            done
            for table in mounts swaps; do
                while read -r source rest; do
                    case "$source" in
                        "$devroot"/*)
                            source=$(readlink -f "$source") || { busy=true; break; }
                            ;;
                    esac
                    case "$source" in
                        "$devroot/$name"|"$devroot/$name"[0-9]*|"$devroot/$name"p[0-9]*) busy=true ;;
                    esac
                done < "$procroot/$table"
            done
            [ "$busy" = false ] || continue
            printf '%s/%s\n' "$devroot" "$name"
            return 0
        done
    done
    return 1
}
