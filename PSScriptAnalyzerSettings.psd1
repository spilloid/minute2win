# Lint settings for tests/ci.ps1. Every exclusion has a reason; anything else at Warning or Error fails CI.
@{
    Rules        = @{
        # WinPE runs Windows PowerShell 5.1: catch PowerShell 7-only syntax before it reaches a target.
        PSUseCompatibleSyntax = @{ Enable = $true; TargetVersions = @('5.1') }
    }
    ExcludeRules = @(
        'PSAvoidUsingWriteHost'                         # console scripts: the screen (and the capture card watching it) is the UI
        'PSAvoidUsingPlainTextForPassword'              # the OOBE baseline password is public by design and forced to change
        'PSAvoidUsingUsernameAndPasswordParams'         # same: New-M2WUnattend fills a template, no secret is handled
        'PSUseShouldProcessForStateChangingFunctions'   # zero-touch by design; New-* helpers here only build strings/objects
        'PSUseSingularNouns'
        'PSUseBOMForUnicodeEncodedFile'
    )
}
