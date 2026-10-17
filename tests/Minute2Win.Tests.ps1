# Pester 5. Covers the engine's pure functions; disk, network and DISM steps are covered by the VM run (tests/vm/).
BeforeAll {
    $repo = Split-Path $PSScriptRoot -Parent
    Import-Module (Join-Path $repo 'engine/Minute2Win.psm1') -Force
    function New-Disk($n, $bus, $gb) { [pscustomobject]@{ Number = $n; BusType = $bus; Size = [int64]$gb * 1GB; FriendlyName = "disk$n" } }
    function Get-Manifest { Get-Content (Join-Path $repo 'manifests/win11-pro.json') -Raw | ConvertFrom-Json }
}

Describe 'scripts parse' {
    It '<_> has no syntax errors' -ForEach @(Get-ChildItem (Split-Path $PSScriptRoot -Parent) -Recurse -Include *.ps1, *.psm1 | ForEach-Object FullName) {
        $errs = $null
        [void][System.Management.Automation.Language.Parser]::ParseFile($_, [ref]$null, [ref]$errs)
        $errs | Should -BeNullOrEmpty
    }
    # Windows PowerShell 5.1 (WinPE, a stock Windows 11) reads a file without a BOM as the ANSI code page, so a UTF-8 em dash
    # inside a string becomes a closing quote and the script stops parsing. pwsh 7, which CI uses, reads UTF-8 and never notices.
    It '<_> is ASCII, or carries a UTF-8 BOM, so Windows PowerShell 5.1 parses it the same' -ForEach @(Get-ChildItem (Split-Path $PSScriptRoot -Parent) -Recurse -Include *.ps1, *.psm1, *.psd1 | ForEach-Object FullName) {
        $bytes = [IO.File]::ReadAllBytes($_)
        $bom = $bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF
        if (-not $bom) { @($bytes | Where-Object { $_ -ge 0x80 }).Count | Should -Be 0 }
    }
}

Describe 'Get-M2WValue' {
    It 'reads nested paths and falls back on missing or null' {
        $o = '{"a":{"b":5,"n":null}}' | ConvertFrom-Json
        Get-M2WValue $o 'a.b' | Should -Be 5
        Get-M2WValue $o 'a.x' 7 | Should -Be 7
        Get-M2WValue $o 'a.n' 'd' | Should -Be 'd'
        Get-M2WValue $o 'z.y.x' 'd' | Should -Be 'd'
    }
}

Describe 'Test-M2WManifest' {
    It 'accepts every shipped manifest' -ForEach @(Get-ChildItem (Join-Path (Split-Path $PSScriptRoot -Parent) 'manifests') -Filter *.json -Recurse | ForEach-Object FullName) {
        Test-M2WManifest (Get-Content $_ -Raw | ConvertFrom-Json) | Should -BeNullOrEmpty
    }
    It 'rejects <name>' -ForEach @(
        @{ name = 'a non-https source'; path = 'image.source'; value = 'smb'; expect = 'not supported' }
        @{ name = 'an http uri'; path = 'image.uri'; value = 'http://x/i.wim'; expect = 'https://' }
        @{ name = 'a short hash'; path = 'image.sha256'; value = 'abc'; expect = 'sha256' }
        @{ name = 'a bad admin user'; path = 'oobe.adminUser'; value = 'bad user!'; expect = 'adminUser' }
        @{ name = 'an unknown join method'; path = 'tenant.joinMethod'; value = 'workgroup'; expect = 'joinMethod' }
        @{ name = 'a negative countdown'; path = 'guard.countdownSeconds'; value = -1; expect = 'countdown' }
        @{ name = 'a negative image size'; path = 'image.sizeBytes'; value = -5; expect = 'sizeBytes' }
        @{ name = 'a wrong schema'; path = 'schemaVersion'; value = 2; expect = 'schemaVersion' }
        @{ name = 'a non-http report url'; path = 'report.url'; value = 'ftp://x'; expect = 'report.url' }
    ) {
        $m = Get-Manifest
        $parts = $path.Split('.')
        $o = $m
        foreach ($p in $parts[0..($parts.Count - 2)]) {
            if (-not $p) { continue }
            if (-not $o.PSObject.Properties[$p]) { $o | Add-Member -NotePropertyName $p -NotePropertyValue ([pscustomobject]@{}) }
            $o = $o.$p
        }
        if ($parts.Count -gt 1 -and -not $o.PSObject.Properties[$parts[-1]]) { $o | Add-Member -NotePropertyName $parts[-1] -NotePropertyValue $null }
        if ($parts.Count -eq 1) { $m.$path = $value } else { $o.($parts[-1]) = $value }
        (Test-M2WManifest $m) -join ' ' | Should -Match $expect
    }
    It 'requires an edition or an index' {
        $m = Get-Manifest; $m.image.PSObject.Properties.Remove('edition')
        (Test-M2WManifest $m) -join ' ' | Should -Match 'edition'
    }
}

Describe 'Select-M2WTargetDisk (zero-touch, never prompts)' {
    It 'takes the lowest-numbered eligible disk' {
        (Select-M2WTargetDisk -Disks @((New-Disk 1 'NVMe' 512), (New-Disk 0 'SATA' 256))).Number | Should -Be 0
    }
    It 'never takes a USB disk (the boot stick) or a virtual file disk' {
        (Select-M2WTargetDisk -Disks @((New-Disk 0 'USB' 512), (New-Disk 1 'File Backed Virtual' 512), (New-Disk 2 'NVMe' 256))).Number | Should -Be 2
    }
    It 'skips disks under the minimum size' {
        (Select-M2WTargetDisk -Disks @((New-Disk 0 'SATA' 32), (New-Disk 1 'SATA' 128))).Number | Should -Be 1
    }
    It 'prefers a fixed disk over an SD/MMC card, but uses eMMC when it is the only disk' {
        (Select-M2WTargetDisk -Disks @((New-Disk 0 'SD' 128), (New-Disk 1 'NVMe' 256))).Number | Should -Be 1
        (Select-M2WTargetDisk -Disks @((New-Disk 0 'MMC' 128), (New-Disk 1 'USB' 256))).Number | Should -Be 0
    }
    It 'honours an eligible override and refuses an ineligible one' {
        (Select-M2WTargetDisk -Disks @((New-Disk 0 'SATA' 256), (New-Disk 1 'NVMe' 256)) -DiskNumber 1).Number | Should -Be 1
        { Select-M2WTargetDisk -Disks @((New-Disk 0 'SATA' 256), (New-Disk 1 'USB' 256)) -DiskNumber 1 } | Should -Throw '*not an eligible*'
    }
    It 'throws when nothing is eligible' {
        { Select-M2WTargetDisk -Disks @((New-Disk 0 'USB' 512)) } | Should -Throw '*No eligible*'
        { Select-M2WTargetDisk -Disks @() } | Should -Throw '*No eligible*'
    }
}

Describe 'Get-M2WDiskpartScript' {
    It 'targets the chosen disk with the GPT layout and staging size' {
        $s = Get-M2WDiskpartScript -DiskNumber 3 -StagingGB 12
        $lines = $s -split "`r`n"
        $lines[0] | Should -Be 'select disk 3'
        $lines | Should -Contain 'convert gpt'
        $lines | Should -Contain 'shrink desired=12288'
        $lines | Should -Contain 'assign letter=S'
        $lines | Should -Contain 'assign letter=W'
        $lines | Should -Contain 'assign letter=Y'
        ($lines -match '^assign letter=[CD]$') | Should -BeNullOrEmpty
        $lines[-1] | Should -Be 'exit'
    }
    It 'runs its steps in order: select, clean, gpt, then the partitions' {
        $lines = (Get-M2WDiskpartScript -DiskNumber 0) -split "`r`n"
        $order = 'select disk 0', 'clean', 'convert gpt', 'create partition efi size=260', 'create partition msr size=16', 'create partition primary', 'shrink desired=12288', 'assign letter=W', 'assign letter=Y'
        $idx = $order | ForEach-Object { [array]::IndexOf($lines, $_) }
        $idx | Should -Not -Contain -1
        ($idx | Sort-Object) -join ',' | Should -Be ($idx -join ',')
    }
}

Describe 'Select-M2WDriveLetters' {
    It 'prefers S, W and Y when they are free' {
        $l = Select-M2WDriveLetters -Used @('C', 'D', 'X')
        "$($l.System)$($l.Windows)$($l.Staging)" | Should -Be 'SWY'
    }
    It 'steps around letters held by anything, e.g. an optical drive on S: and a USB stick on Y:' {
        $l = Select-M2WDriveLetters -Used @('C', 'S', 'X', 'Y')
        $l.System | Should -Not -BeIn @('S', 'Y', 'C', 'X')
        $l.Windows | Should -Be 'W'
        $l.Staging | Should -Not -BeIn @('S', 'Y', 'C', 'X', 'W', $l.System)
        @($l.System, $l.Windows, $l.Staging) | Sort-Object -Unique | Should -HaveCount 3
    }
    It 'never picks A or B and throws when nothing is free' {
        $all = [char[]](67..90) | ForEach-Object { [string]$_ }
        { Select-M2WDriveLetters -Used $all } | Should -Throw '*No free drive letters*'
    }
    It 'feeds the chosen letters into the diskpart script' {
        $lines = (Get-M2WDiskpartScript -DiskNumber 0 -System 'R' -Windows 'T' -Staging 'U') -split "`r`n"
        $lines | Should -Contain 'assign letter=R'
        $lines | Should -Contain 'assign letter=T'
        $lines | Should -Contain 'assign letter=U'
    }
}

Describe 'Test-M2WCapacity (checked before the wipe)' {
    It 'passes the shipped profile: 8.16 GB image, 12 GB staging, 256 GB disk' {
        { Test-M2WCapacity -ImageBytes 8155984950 -StagingGB 12 -DiskBytes 256GB } | Should -Not -Throw
    }
    It 'refuses staging smaller than the image, naming the size needed' {
        { Test-M2WCapacity -ImageBytes 8155984950 -StagingGB 1 -DiskBytes 256GB } | Should -Throw '*at least 9*'
    }
    It 'refuses a disk with no room for Windows after staging' {
        { Test-M2WCapacity -ImageBytes 1GB -StagingGB 40 -DiskBytes 64GB } | Should -Throw '*too small*'
    }
    It 'fails closed when the image size is unknown' {
        { Test-M2WCapacity -ImageBytes -1 -StagingGB 12 -DiskBytes 256GB } | Should -Throw '*size unknown*'
        { Test-M2WCapacity -ImageBytes 0 -StagingGB 12 -DiskBytes 256GB } | Should -Throw '*size unknown*'
    }
}

Describe 'Add-M2WSetupComplete' {
    It 'prepends its line and keeps an existing script byte for byte' {
        $f = Join-Path ([IO.Path]::GetTempPath()) "sc-$([guid]::NewGuid()).cmd"
        $old = [byte[]](0x40, 0x65, 0x63, 0x68, 0x6f, 0x20, 0xC3, 0xA9, 0x0D, 0x0A, 0x65, 0x78, 0x69, 0x74, 0x0D, 0x0A) # "@echo " plus e-acute as UTF-8 (C3 A9), then "exit"
        [IO.File]::WriteAllBytes($f, $old)
        Add-M2WSetupComplete $f
        $new = [IO.File]::ReadAllBytes($f)
        $line = [Text.Encoding]::ASCII.GetBytes((Get-M2WSetupCompleteLine) + "`r`n")
        $new.Length | Should -Be ($line.Length + $old.Length)
        [Linq.Enumerable]::SequenceEqual([byte[]]$new[$line.Length..($new.Length - 1)], $old) | Should -BeTrue
        Remove-Item $f
    }
    It 'creates the script when there is none' {
        $f = Join-Path ([IO.Path]::GetTempPath()) "sc-$([guid]::NewGuid()).cmd"
        Add-M2WSetupComplete $f
        (Get-Content $f -Raw) | Should -Match 'Start-FirstBoot\.ps1'
        Remove-Item $f
    }
}

Describe 'Resolve-M2WImageIndex' {
    BeforeAll {
        function Get-Images { @([pscustomobject]@{ ImageIndex = 1; ImageName = 'Windows 11 Home' }, [pscustomobject]@{ ImageIndex = 6; ImageName = 'Windows 11 Pro ' }) }
    }
    It 'matches the edition by name, trimming whitespace' { Resolve-M2WImageIndex -Images (Get-Images) -Edition 'Windows 11 Pro' | Should -Be 6 }
    It 'falls back to the index' { Resolve-M2WImageIndex -Images (Get-Images) -Index 1 | Should -Be 1 }
    It 'throws on a missing edition, naming what is present' { { Resolve-M2WImageIndex -Images (Get-Images) -Edition 'Windows 11 Pro N' } | Should -Throw '*Windows 11 Home*' }
    It 'throws on a missing index' { { Resolve-M2WImageIndex -Images (Get-Images) -Index 9 } | Should -Throw }
}

Describe 'New-M2WUnattend' {
    It 'fills the shipped template into well-formed XML with the password escaped' {
        $t = Get-Content (Join-Path (Split-Path $PSScriptRoot -Parent) 'templates/unattend-oobe.xml') -Raw
        $x = New-M2WUnattend -Template $t -User 'tech' -Password 'a<b&"c'
        $x | Should -Not -Match '__USER__|__PASSWORD__'
        $doc = [xml]$x
        $ns = @{ u = 'urn:schemas-microsoft-com:unattend' }
        (Select-Xml -Xml $doc -XPath '//u:LocalAccount/u:Password/u:Value' -Namespace $ns).Node.InnerText | Should -Be 'a<b&"c'
        (Select-Xml -Xml $doc -XPath '//u:LocalAccount/u:Name' -Namespace $ns).Node.InnerText | Should -Be 'tech'
    }
}

Describe 'Get-M2WSetupCompleteLine' {
    It 'runs the staged first-boot script and logs to Windows\Logs\minute2win' {
        $l = Get-M2WSetupCompleteLine
        $l | Should -Match 'ProgramData\\minute2win\\Start-FirstBoot\.ps1'
        $l | Should -Match 'Logs\\minute2win\\setupcomplete\.log'
    }
}

Describe 'boot loader fails closed without the selected manifest' {
    It 'stops before downloading or launching the installer when injection is absent' {
        Mock Test-Path { $false }
        { & (Join-Path $repo 'boot/install.ps1') -Base 'http://127.0.0.1:1' } | Should -Throw '*Boot manifest was not injected*'
    }
}
