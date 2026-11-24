---
title: Reimage workstations from one Windows PC
---

# Reimage workstations from one Windows PC

A repair bench, classroom, or lab can use one Windows PC to reimage a row of workstations. Run **minute2win** for the Windows profiles and live deployment board. Run its sibling **kvm-it** on the same PC to watch the target and use its keyboard, mouse, and boot menu from the bench.

Build the Windows profile once. For each workstation, verify its Ethernet MAC, arm it, and pick a boot device. WinPE takes it from there. While Windows installs on that machine, move on to the next one.

## Set up the bench once

1. **Install the two tools on your Windows PC.** Unzip the signed Windows x64 minute2win release and run `minute2win-server.exe`. The console opens in your browser. The optional tray icon gives you a shortcut back to it. Install kvm-it on the same PC and connect its adapter to the workstation you want to control. [minute2win downloads](https://github.com/spilloid/minute2win/releases) · [kvm-it downloads](https://github.com/spilloid/kvm-it/releases) · [Tray icon](DESKTOP.md)
2. **Build and host the Windows boot files.** Use the Windows ADK and WinPE add-on to build the boot image, then stage the minute2win scripts. Serve those files and your licensed `install.wim` over HTTPS. Cloudflare R2 with a custom domain works for this. Add the wired NIC and storage drivers your fleet needs to WinPE, especially if the bench uses USB Ethernet docks. [Boot assets](BOOT_ASSETS.md)
3. **Create a profile and start minute2win on your bench LAN.** Copy a Windows profile into `profiles` and set the image URL, SHA-256, and edition, such as Windows 11 Pro. Use your own media URL and operator token in the example below. Keep the server reachable from the workstations on a trusted deployment network. [Getting started](GETTING_STARTED.md) · [Profile reference](MANIFESTS.md)

```powershell
$env:M2W_TOKEN = 'replace-with-your-operator-token'
.\minute2win-server.exe -listen :8080 -manifests .\profiles -data .\data `
  -image-base https://images.example.net/windows11
```

The HTTPS host serves the big files. minute2win on your bench PC decides which armed machine gets which image and shows you where the install is. You can keep the same Windows image when you move the deployment server or add another bench.

## Choose how each workstation boots

- **Firmware PXE.** Pick IPv4 network boot in the workstation's boot menu. Use the network's existing PXE setup or add minute2win's built-in PXE and TFTP service on the bench LAN. Both paths should give iPXE the server's per-MAC boot URL. [PXE setup](SERVER.md)
- **kvm-it USB boot drive.** If the workstation offers UEFI USB boot and iPXE can see its wired Ethernet adapter, select the kvm-it drive in the boot menu. Build the drive image with the `autoexec.ipxe` below in place of the stock demo script, flash it through the adapter's COM port, and turn on **Boot drive** in kvm-it. This path uses ordinary DHCP. [kvm-it boot drive build](https://github.com/spilloid/kvm-it/blob/main/docs/developing.md#the-boot-drive-image)

Replace the address below with your bench PC's LAN address. The keypress starts the USB boot. If you leave it alone, the workstation can fall through to its installed disk.

```ipxe
#!ipxe
prompt --timeout 5000 Press a key to check minute2win for an armed install || exit 1
dhcp || exit 1
chain http://192.168.1.50:8080/boot/${netX/mac} || exit 1
exit 1
```

If a dock works in firmware PXE but disappears when iPXE starts from USB, use firmware PXE with that dock. For hardware that cannot reach the network in either path, use Windows installer media. [Boot path setup](GETTING_STARTED.md)

## Arm a workstation and watch it install

1. Start with a disposable workstation or VM. Confirm the MAC of the interface it will boot through and the internal disk you are about to erase.
2. In minute2win, select your Windows profile and arm that MAC. **Arming authorizes erasing the selected internal disk on the next boot into minute2win.**
3. Use kvm-it or the workstation's own keyboard to select the boot device. The board shows the deployment once the one-time arm is claimed.
4. Verify the new Windows desktop, then boot once more without arming. The workstation should carry on to Windows. Use that same profile for the rest of the bench.

A recorded Windows 11 VM deployment took about six minutes. Image size, disk speed, and shared bandwidth determine how quickly a batch finishes. The time you save on every machine is the setup you already did once. [Verification record](STATUS.md)
