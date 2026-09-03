package steam

import (
	"fmt"
	"strconv"

	"github.com/3219378872/rent-auto/backend/internal/platform"
)

// Steam WebAPI responses carry the application-level result code in the
// undocumented `X-eresult` response header — HTTP 200 does NOT mean success.
// Verified against upstream steampy (check_error + STEAM_ERROR_CODES):
// acceptable codes are {1 OK, 22 Pending}; per-endpoint ignores exist
// (UpdateAuthSessionWithSteamGuardCode tolerates 29 DuplicateRequest,
// upstream ignore_error_num=[29]). Not checking this header swallows real
// login failures, which then surface as misleading downstream errors
// (2026-08-27: "empty refresh token after poll" for a rejected TOTP code).
var eresultNames = map[int]string{
	1: "OK", 2: "Fail", 3: "NoConnection", 4: "NoConnectionRetry",
	5: "InvalidPassword", 6: "LoggedInElsewhere", 7: "InvalidProtocolVer",
	8: "InvalidParam", 9: "FileNotFound", 10: "Busy", 11: "InvalidState",
	12: "InvalidName", 13: "InvalidEmail", 14: "DuplicateName",
	15: "AccessDenied", 16: "Timeout", 17: "Banned", 18: "AccountNotFound",
	19: "InvalidSteamID", 20: "ServiceUnavailable", 21: "NotLoggedOn",
	22: "Pending", 23: "EncryptionFailure", 24: "InsufficientPrivilege",
	25: "LimitExceeded", 26: "Revoked", 27: "Expired", 28: "AlreadyRedeemed",
	29: "DuplicateRequest", 30: "AlreadyOwned", 31: "IPNotFound",
	32: "PersistFailed", 33: "LockingFailed", 34: "LogonSessionReplaced",
	35: "ConnectFailed", 36: "HandshakeFailed", 37: "IOFailure",
	38: "RemoteDisconnect", 39: "ShoppingCartNotFound", 40: "Blocked",
	41: "Ignored", 42: "NoMatch", 43: "AccountDisabled", 44: "ServiceReadOnly",
	45: "AccountNotFeatured", 46: "AdministratorOK", 47: "ContentVersion",
	48: "TryAnotherCM", 49: "PasswordRequiredToKickSession",
	50: "AlreadyLoggedInElsewhere", 51: "Suspended", 52: "Cancelled",
	53: "DataCorruption", 54: "DiskFull", 55: "RemoteCallFailed",
	56: "PasswordUnset", 57: "ExternalAccountUnlinked", 58: "PSNTicketInvalid",
	59: "ExternalAccountAlreadyLinked", 60: "RemoteFileConflict",
	61: "IllegalPassword", 62: "SameAsPreviousValue", 63: "AccountLogonDenied",
	64: "CannotUseOldPassword", 65: "InvalidLoginAuthCode",
	66: "AccountLogonDeniedNoMail", 67: "HardwareNotCapableOfIPT",
	68: "IPTInitError", 69: "ParentalControlRestricted", 70: "FacebookQueryError",
	71: "ExpiredLoginAuthCode", 72: "IPLoginRestrictionFailed",
	73: "AccountLockedDown", 74: "AccountLogonDeniedVerifiedEmailRequired",
	75: "NoMatchingURL", 76: "BadResponse", 77: "RequirePasswordReEntry",
	78: "ValueOutOfRange", 79: "UnexpectedError", 80: "Disabled",
	81: "InvalidCEGSubmission", 82: "RestrictedDevice", 83: "RegionLocked",
	84: "RateLimitExceeded", 85: "AccountLoginDeniedNeedTwoFactor",
	86: "ItemDeleted", 87: "AccountLoginDeniedThrottle",
	88: "TwoFactorCodeMismatch", 89: "TwoFactorActivationCodeMismatch",
	90: "AccountAssociatedToMultiplePartners", 91: "NotModified",
	92: "NoMobileDevice", 93: "TimeNotSynced", 94: "SmsCodeFailed",
	95: "AccountLimitExceeded", 96: "AccountActivityLimitExceeded",
	97: "PhoneActivityLimitExceeded", 98: "RefundToWallet",
	99: "EmailSendFailure", 100: "NotSettled", 101: "NeedCaptcha",
	102: "GSLTDenied", 103: "GSOwnerDenied", 104: "InvalidItemType",
	105: "IPBanned", 106: "GSLTExpired", 107: "InsufficientFunds",
	108: "TooManyPending", 109: "NoSiteLicensesFound",
	110: "WGNetworkSendExceeded", 111: "AccountNotFriends",
	112: "LimitedUserAccount", 113: "CantRemoveItem", 114: "AccountDeleted",
	115: "ExistingUserCancelledLicense", 116: "CommunityCooldown",
	117: "NoLauncherSpecified", 118: "MustAgreeToSSA", 119: "LauncherMigrated",
}

// eresultAuthExpired collects codes that mean the session/credential is dead
// and only a fresh login helps: wrong/rejected secrets, login throttles and
// limit-exceeded on the auth surface (5/25/84/85/87/88). Callers branch on
// platform.ErrAuthExpired to rebuild the session instead of retrying blind.
var eresultAuthExpired = map[int]bool{
	5:  true, // InvalidPassword
	25: true, // LimitExceeded
	84: true, // RateLimitExceeded
	85: true, // AccountLoginDeniedNeedTwoFactor
	87: true, // AccountLoginDeniedThrottle
	88: true, // TwoFactorCodeMismatch
}

// eresultRateLimited collects transient pressure codes worth a scheduler
// cooldown (platform.ErrRateLimited), distinct from the auth-throttle family
// above: Busy, account/phone activity caps, pending overflow and cooldowns.
var eresultRateLimited = map[int]bool{
	10:  true, // Busy
	95:  true, // AccountLimitExceeded
	96:  true, // AccountActivityLimitExceeded
	97:  true, // PhoneActivityLimitExceeded
	108: true, // TooManyPending
	110: true, // WGNetworkSendExceeded
	116: true, // CommunityCooldown
}

// checkEresult turns the X-eresult header value into an error.
// absent (<0) → nil (non-WebAPI responses / redirect hops without the header);
// {1, 22} → nil; explicit ignore list (e.g. 29 for guard updates) → nil.
// Anything else maps to a unified sentinel (auth vs rate, see above) while
// keeping the `eresult=<n> <Name>` text for logs; unknown codes stay generic.
func checkEresult(method string, eresult int, ignore ...int) error {
	if eresult < 0 || eresult == 1 || eresult == 22 {
		return nil
	}
	for _, ig := range ignore {
		if eresult == ig {
			return nil
		}
	}
	name := strconv.Itoa(eresult)
	if s, ok := eresultNames[eresult]; ok {
		name = s
	}
	base := fmt.Errorf("steam: %s failed (eresult=%d %s)", method, eresult, name)
	switch {
	case eresultAuthExpired[eresult]:
		return fmt.Errorf("%w: %w", platform.ErrAuthExpired, base)
	case eresultRateLimited[eresult]:
		return fmt.Errorf("%w: %w", platform.ErrRateLimited, base)
	default:
		return base
	}
}
