package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	verifyVerb           = "verify"
	testPublicKeyPath    = "/path/to/key.pub"
	testSignaturePath    = "/path/to/sig"
	testIdentity         = "test@example.com"
	testIdentityProvider = "https://accounts.google.com"
	certificateMethod    = "certificate"
	sigstoreMethod       = "sigstore"
	keyMethod            = "key"
	testSignatureArg     = "--signature=/path/to/sig"
	minimalSignatureArg  = "--signature=/sig"
	identityFlag         = "--identity"
	identityProviderFlag = "--identity_provider"
	testModelPath        = "/path/to/model"
	publicKeyFlag        = "--public_key"
	ignorePathsFlag      = "--ignore-paths"
)

func TestParseArgs_Sigstore(t *testing.T) {
	args := []string{
		verifyVerb, sigstoreMethod,
		testSignatureArg,
		identityFlag, testIdentity,
		identityProviderFlag, testIdentityProvider,
		testModelPath,
	}

	cfg, err := ParseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, sigstoreMethod, cfg.Method)
	assert.Equal(t, testModelPath, cfg.ModelPath)
	assert.Equal(t, testSignaturePath, cfg.SignaturePath)
	assert.Equal(t, testIdentity, cfg.Identity)
	assert.Equal(t, testIdentityProvider, cfg.IdentityProvider)
}

func TestParseArgs_Key(t *testing.T) {
	args := []string{
		verifyVerb, keyMethod,
		testSignatureArg,
		publicKeyFlag, testPublicKeyPath,
		testModelPath,
	}

	cfg, err := ParseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, keyMethod, cfg.Method)
	assert.Equal(t, testModelPath, cfg.ModelPath)
	assert.Equal(t, testSignaturePath, cfg.SignaturePath)
	assert.Equal(t, testPublicKeyPath, cfg.PublicKeyPath)
}

func TestParseArgs_Certificate(t *testing.T) {
	args := []string{
		verifyVerb, certificateMethod,
		testSignatureArg,
		"--certificate_chain", "/path/to/ca.pem",
		testModelPath,
	}

	cfg, err := ParseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, certificateMethod, cfg.Method)
	assert.Equal(t, testModelPath, cfg.ModelPath)
	assert.Equal(t, testSignaturePath, cfg.SignaturePath)
	assert.Equal(t, "/path/to/ca.pem", cfg.CertificateChainPath)
}

func TestParseArgs_WithTrustConfig(t *testing.T) {
	args := []string{
		verifyVerb, sigstoreMethod,
		testSignatureArg,
		identityFlag, testIdentity,
		identityProviderFlag, testIdentityProvider,
		"--trust_config", "/path/to/trust.json",
		testModelPath,
	}

	cfg, err := ParseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "/path/to/trust.json", cfg.TrustConfigPath)
}

func TestParseArgs_WithIgnorePaths(t *testing.T) {
	args := []string{
		verifyVerb, keyMethod,
		testSignatureArg,
		publicKeyFlag, testPublicKeyPath,
		ignorePathsFlag, "/tmp",
		ignorePathsFlag, "/cache",
		testModelPath,
	}

	cfg, err := ParseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp", "/cache"}, cfg.IgnorePaths)
}

func TestParseArgs_WithBooleanFlags(t *testing.T) {
	args := []string{
		verifyVerb, keyMethod,
		testSignatureArg,
		publicKeyFlag, testPublicKeyPath,
		"--ignore-git-paths",
		"--no-ignore_unsigned_files",
		"--allow_symlinks",
		testModelPath,
	}

	cfg, err := ParseArgs(args)
	require.NoError(t, err)
	require.NotNil(t, cfg.IgnoreGitPaths)
	assert.True(t, *cfg.IgnoreGitPaths)
	require.NotNil(t, cfg.IgnoreUnsignedFiles)
	assert.False(t, *cfg.IgnoreUnsignedFiles)
	require.NotNil(t, cfg.AllowSymlinks)
	assert.True(t, *cfg.AllowSymlinks)
}

func TestParseArgs_NegatedBooleanFlags(t *testing.T) {
	args := []string{
		verifyVerb, keyMethod,
		testSignatureArg,
		publicKeyFlag, testPublicKeyPath,
		"--no-ignore-git-paths",
		"--ignore_unsigned_files",
		testModelPath,
	}

	cfg, err := ParseArgs(args)
	require.NoError(t, err)
	require.NotNil(t, cfg.IgnoreGitPaths)
	assert.False(t, *cfg.IgnoreGitPaths)
	require.NotNil(t, cfg.IgnoreUnsignedFiles)
	assert.True(t, *cfg.IgnoreUnsignedFiles)
	assert.Nil(t, cfg.AllowSymlinks)
}

func TestParseArgs_NilBooleans(t *testing.T) {
	args := []string{
		verifyVerb, keyMethod,
		testSignatureArg,
		publicKeyFlag, testPublicKeyPath,
		testModelPath,
	}

	cfg, err := ParseArgs(args)
	require.NoError(t, err)
	assert.Nil(t, cfg.IgnoreGitPaths)
	assert.Nil(t, cfg.IgnoreUnsignedFiles)
	assert.Nil(t, cfg.AllowSymlinks)
}

func TestParseArgs_TooFewArgs(t *testing.T) {
	_, err := ParseArgs([]string{verifyVerb, sigstoreMethod})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected at least 3 arguments")
}

func TestParseArgs_InvalidVerb(t *testing.T) {
	_, err := ParseArgs([]string{"sign", sigstoreMethod, "/model"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected first argument to be 'verify'")
}

func TestParseArgs_InvalidMethod(t *testing.T) {
	_, err := ParseArgs([]string{verifyVerb, "unknown", "/model"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown verification method")
}

func TestParseArgs_MissingModelPath(t *testing.T) {
	args := []string{
		verifyVerb, keyMethod,
		testSignatureArg,
		publicKeyFlag, testPublicKeyPath,
	}
	_, err := ParseArgs(args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model path is required")
}

func TestParseArgs_MissingFlagValue(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "missing identity value",
			args: []string{verifyVerb, sigstoreMethod, minimalSignatureArg, identityFlag},
		},
		{
			name: "missing identity_provider value",
			args: []string{verifyVerb, sigstoreMethod, minimalSignatureArg, identityProviderFlag},
		},
		{
			name: "missing public_key value",
			args: []string{verifyVerb, keyMethod, minimalSignatureArg, publicKeyFlag},
		},
		{
			name: "missing certificate_chain value",
			args: []string{verifyVerb, certificateMethod, minimalSignatureArg, "--certificate_chain"},
		},
		{
			name: "missing trust_config value",
			args: []string{verifyVerb, sigstoreMethod, minimalSignatureArg, "--trust_config"},
		},
		{
			name: "missing ignore-paths value",
			args: []string{verifyVerb, keyMethod, minimalSignatureArg, ignorePathsFlag},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseArgs(tc.args)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "requires a value")
		})
	}
}
