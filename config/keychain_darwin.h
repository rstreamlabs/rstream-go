// See LICENSE file in the project root for license information.

//go:build darwin && cgo

#ifndef RSTREAM_CONFIG_KEYCHAIN_DARWIN_H
#define RSTREAM_CONFIG_KEYCHAIN_DARWIN_H

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stddef.h>

enum
{
	rsAlgRSAPKCS1SHA1 = 1,
	rsAlgRSAPKCS1SHA256 = 2,
	rsAlgRSAPKCS1SHA384 = 3,
	rsAlgRSAPKCS1SHA512 = 4,
	rsAlgRSAPSSSHA256 = 5,
	rsAlgRSAPSSSHA384 = 6,
	rsAlgRSAPSSSHA512 = 7,
	rsAlgECDSASHA1 = 8,
	rsAlgECDSASHA256 = 9,
	rsAlgECDSASHA384 = 10,
	rsAlgECDSASHA512 = 11
};

OSStatus rs_copy_generic_password(const char *service, const char *account, CFDataRef *outData);
OSStatus rs_store_generic_password(const char *service, const char *account, const unsigned char *token, size_t tokenLen);
OSStatus rs_delete_generic_password(const char *service, const char *account);
OSStatus rs_copy_identity_key_by_sha256(const unsigned char *fingerprint, SecKeyRef *outKey, CFDataRef *outCertData);
OSStatus rs_sec_key_sign(SecKeyRef key, int algorithm, const unsigned char *digest, size_t digestLen, CFDataRef *outSignature);
CFIndex rs_cfdata_len(CFDataRef data);
const UInt8 *rs_cfdata_bytes(CFDataRef data);

#endif
