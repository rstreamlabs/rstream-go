// See LICENSE file in the project root for license information.

//go:build darwin && cgo

#include "keychain_darwin.h"

#include <CommonCrypto/CommonDigest.h>
#include <string.h>

static CFStringRef rs_cfstr(const char *value) {
	if (value == NULL || value[0] == 0) {
		return NULL;
	}
	return CFStringCreateWithCString(kCFAllocatorDefault, value, kCFStringEncodingUTF8);
}

static void rs_dict_set_cstr(CFMutableDictionaryRef dict, const void *key, const char *value) {
	CFStringRef str = rs_cfstr(value);
	if (str != NULL) {
		CFDictionarySetValue(dict, key, str);
		CFRelease(str);
	}
}

static OSStatus rs_create_current_app_access(const char *description, SecAccessRef *outAccess) {
	CFStringRef descriptor = rs_cfstr(description);
	if (descriptor == NULL) {
		descriptor = CFSTR("rstream credential");
		CFRetain(descriptor);
	}
	// SecAccess ACLs are the hardened path for signed CLI binaries. Data
	// Protection keychain access groups require app entitlements and do not fit
	// the Developer ID/Homebrew-style CLI distribution model.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	OSStatus status = SecAccessCreate(descriptor, NULL, outAccess);
#pragma clang diagnostic pop
	CFRelease(descriptor);
	return status;
}

static CFMutableDictionaryRef rs_base_password_query(const char *service, const char *account) {
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		kCFAllocatorDefault,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	rs_dict_set_cstr(query, kSecAttrService, service);
	rs_dict_set_cstr(query, kSecAttrAccount, account);
	return query;
}

OSStatus rs_copy_generic_password(const char *service, const char *account, CFDataRef *outData) {
	CFMutableDictionaryRef query = rs_base_password_query(service, account);
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	OSStatus status = SecItemCopyMatching(query, (CFTypeRef *)outData);
	CFRelease(query);
	return status;
}

OSStatus rs_store_generic_password(const char *service, const char *account, const unsigned char *token, size_t tokenLen) {
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, token, (CFIndex)tokenLen);
	if (data == NULL) {
		return errSecAllocate;
	}
	SecAccessRef access = NULL;
	OSStatus status = rs_create_current_app_access("rstream token", &access);
	if (status != errSecSuccess) {
		CFRelease(data);
		return status;
	}
	CFMutableDictionaryRef item = rs_base_password_query(service, account);
	CFDictionarySetValue(item, kSecValueData, data);
	CFDictionarySetValue(item, kSecAttrAccess, access);
	status = SecItemAdd(item, NULL);
	CFRelease(item);
	if (status == errSecDuplicateItem) {
		CFMutableDictionaryRef query = rs_base_password_query(service, account);
		CFMutableDictionaryRef update = CFDictionaryCreateMutable(
			kCFAllocatorDefault,
			0,
			&kCFTypeDictionaryKeyCallBacks,
			&kCFTypeDictionaryValueCallBacks);
		CFDictionarySetValue(update, kSecValueData, data);
		status = SecItemUpdate(query, update);
		CFRelease(update);
		CFRelease(query);
	}
	CFRelease(access);
	CFRelease(data);
	return status;
}

OSStatus rs_delete_generic_password(const char *service, const char *account) {
	CFMutableDictionaryRef query = rs_base_password_query(service, account);
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status;
}

static CFMutableDictionaryRef rs_base_identity_query(void) {
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		kCFAllocatorDefault,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(query, kSecClass, kSecClassIdentity);
	CFDictionarySetValue(query, kSecReturnRef, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitAll);
	return query;
}

OSStatus rs_copy_identity_key_by_sha256(const unsigned char *fingerprint, SecKeyRef *outKey, CFDataRef *outCertData) {
	CFMutableDictionaryRef query = rs_base_identity_query();
	CFArrayRef results = NULL;
	OSStatus status = SecItemCopyMatching(query, (CFTypeRef *)&results);
	CFRelease(query);
	if (status != errSecSuccess) {
		return status;
	}
	CFIndex count = CFArrayGetCount(results);
	for (CFIndex i = 0; i < count; i++) {
		SecIdentityRef identity = (SecIdentityRef)CFArrayGetValueAtIndex(results, i);
		SecCertificateRef cert = NULL;
		status = SecIdentityCopyCertificate(identity, &cert);
		if (status != errSecSuccess || cert == NULL) {
			continue;
		}
		CFDataRef certData = SecCertificateCopyData(cert);
		CFRelease(cert);
		if (certData == NULL) {
			continue;
		}
		unsigned char digest[CC_SHA256_DIGEST_LENGTH];
		CC_SHA256(CFDataGetBytePtr(certData), (CC_LONG)CFDataGetLength(certData), digest);
		if (memcmp(digest, fingerprint, CC_SHA256_DIGEST_LENGTH) == 0) {
			SecKeyRef key = NULL;
			status = SecIdentityCopyPrivateKey(identity, &key);
			if (status == errSecSuccess && key != NULL) {
				*outKey = key;
				*outCertData = certData;
				CFRelease(results);
				return errSecSuccess;
			}
		}
		CFRelease(certData);
	}
	CFRelease(results);
	return errSecItemNotFound;
}

static CFStringRef rs_sign_algorithm(int algorithm) {
	switch (algorithm) {
	case rsAlgRSAPKCS1SHA1:
		return kSecKeyAlgorithmRSASignatureDigestPKCS1v15SHA1;
	case rsAlgRSAPKCS1SHA256:
		return kSecKeyAlgorithmRSASignatureDigestPKCS1v15SHA256;
	case rsAlgRSAPKCS1SHA384:
		return kSecKeyAlgorithmRSASignatureDigestPKCS1v15SHA384;
	case rsAlgRSAPKCS1SHA512:
		return kSecKeyAlgorithmRSASignatureDigestPKCS1v15SHA512;
	case rsAlgRSAPSSSHA256:
		return kSecKeyAlgorithmRSASignatureDigestPSSSHA256;
	case rsAlgRSAPSSSHA384:
		return kSecKeyAlgorithmRSASignatureDigestPSSSHA384;
	case rsAlgRSAPSSSHA512:
		return kSecKeyAlgorithmRSASignatureDigestPSSSHA512;
	case rsAlgECDSASHA1:
		return kSecKeyAlgorithmECDSASignatureDigestX962SHA1;
	case rsAlgECDSASHA256:
		return kSecKeyAlgorithmECDSASignatureDigestX962SHA256;
	case rsAlgECDSASHA384:
		return kSecKeyAlgorithmECDSASignatureDigestX962SHA384;
	case rsAlgECDSASHA512:
		return kSecKeyAlgorithmECDSASignatureDigestX962SHA512;
	default:
		return NULL;
	}
}

OSStatus rs_sec_key_sign(SecKeyRef key, int algorithm, const unsigned char *digest, size_t digestLen, CFDataRef *outSignature) {
	CFStringRef secAlgorithm = rs_sign_algorithm(algorithm);
	if (secAlgorithm == NULL) {
		return errSecParam;
	}
	if (!SecKeyIsAlgorithmSupported(key, kSecKeyOperationTypeSign, secAlgorithm)) {
		return errSecParam;
	}
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, digest, (CFIndex)digestLen);
	if (data == NULL) {
		return errSecAllocate;
	}
	CFErrorRef error = NULL;
	CFDataRef signature = SecKeyCreateSignature(key, secAlgorithm, data, &error);
	CFRelease(data);
	if (error != NULL) {
		CFRelease(error);
	}
	if (signature == NULL) {
		return errSecAuthFailed;
	}
	*outSignature = signature;
	return errSecSuccess;
}

CFIndex rs_cfdata_len(CFDataRef data) {
	return CFDataGetLength(data);
}

const UInt8 *rs_cfdata_bytes(CFDataRef data) {
	return CFDataGetBytePtr(data);
}
