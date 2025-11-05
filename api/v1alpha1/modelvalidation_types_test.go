package v1alpha1

import (
	"context"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ModelValidation", func() {

	// A helper function to generate a valid ModelValidation object for Sigstore.
	generateSigstoreObject := func(name string) *ModelValidation {
		return &ModelValidation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: ModelValidationSpec{
				Model: Model{
					Path:          "/path/to/model.onnx",
					SignaturePath: "/path/to/model.onnx.sig",
				},
				Config: ValidationConfig{
					SigstoreConfig: &SigstoreConfig{
						CertificateIdentity:   "email:test@example.com",
						CertificateOidcIssuer: "https://accounts.google.com",
					},
				},
			},
		}
	}

	// A helper function to generate a valid ModelValidation object for PKI.
	generatePkiObject := func(name string) *ModelValidation {
		return &ModelValidation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: ModelValidationSpec{
				Model: Model{
					Path:          "/path/to/model.onnx",
					SignaturePath: "/path/to/model.onnx.sig",
				},
				Config: ValidationConfig{
					PkiConfig: &PkiConfig{
						CertificateAuthority: "/path/to/ca.pem",
					},
				},
			},
		}
	}

	// A helper function to generate a valid ModelValidation object for PublicKey.
	generatePublicKeyObject := func(name string) *ModelValidation {
		return &ModelValidation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: ModelValidationSpec{
				Model: Model{
					Path:          "/path/to/model.onnx",
					SignaturePath: "/path/to/model.onnx.sig",
				},
				Config: ValidationConfig{
					PublicKeyConfig: &PublicKeyConfig{
						KeyPath: "/path/to/publickey.pem",
					},
				},
			},
		}
	}

	Context("ModelValidationSpec", func() {
		It("can be created and fetched successfully for Sigstore config", func() {
			created := generateSigstoreObject("mv-create")
			Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

			fetched := &ModelValidation{}
			Expect(k8sClient.Get(context.Background(), getKey(created), fetched)).To(Succeed())
			Expect(fetched).To(Equal(created))
		})

		It("can be created and fetched successfully for PKI config", func() {
			created := generatePkiObject("mv-create-pki")
			Expect(k8sClient.Create(context.Background(), created)).To(Succeed())
		})

		It("can be created and fetched successfully for PublicKey config", func() {
			created := generatePublicKeyObject("mv-create-publickey")
			Expect(k8sClient.Create(context.Background(), created)).To(Succeed())
		})

		It("can be updated with allowed fields", func() {
			created := generateSigstoreObject("mv-update")
			Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

			fetched := &ModelValidation{}
			Expect(k8sClient.Get(context.Background(), getKey(created), fetched)).To(Succeed())
			Expect(fetched).To(Equal(created))

			// Status is not immutable and can be updated
			fetched.Status.Conditions = []metav1.Condition{
				{
					Type:               "Ready",
					Status:             "True",
					LastTransitionTime: metav1.Now(),
					Reason:             "ValidationSuccess",
					Message:            "Model signature is valid",
				},
			}
			Expect(k8sClient.Status().Update(context.Background(), fetched)).To(Succeed())
		})

		It("can be deleted", func() {
			created := generateSigstoreObject("mv-delete")
			Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

			Expect(k8sClient.Delete(context.Background(), created)).To(Succeed())
			Expect(k8sClient.Get(context.Background(), getKey(created), created)).ToNot(Succeed())
		})

		Context("is validated", func() {
			It("rejects an empty Model path", func() {
				invalidObject := generateSigstoreObject("model-path-invalid")
				invalidObject.Spec.Model.Path = ""

				err := k8sClient.Create(context.Background(), invalidObject)
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("spec.model.path: Invalid value: \"\"")))
			})

			It("rejects an empty Signature path", func() {
				invalidObject := generateSigstoreObject("signature-path-invalid")
				invalidObject.Spec.Model.SignaturePath = ""

				err := k8sClient.Create(context.Background(), invalidObject)
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("spec.model.signaturePath: Invalid value: \"\"")))
			})

			It("rejects multiple configs (XValidation violation)", func() {
				invalidObject := generateSigstoreObject("xor-violation-multi")
				invalidObject.Spec.Config.PkiConfig = &PkiConfig{
					CertificateAuthority: "/path/to/ca.pem",
				}

				err := k8sClient.Create(context.Background(), invalidObject)
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("exactly one validation method must be specified")))
			})

			It("rejects zero configs (XValidation violation)", func() {
				invalidObject := generateSigstoreObject("xor-violation-zero")
				invalidObject.Spec.Config.SigstoreConfig = nil

				err := k8sClient.Create(context.Background(), invalidObject)
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("exactly one validation method must be specified")))
			})

			It("rejects a missing required field in SigstoreConfig", func() {
				invalidObject := generateSigstoreObject("sigstore-missing-field")
				invalidObject.Spec.Config.SigstoreConfig.CertificateIdentity = ""

				err := k8sClient.Create(context.Background(), invalidObject)
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("spec.config.sigstoreConfig.certificateIdentity: Required value")))
			})

			It("rejects a missing required field in PkiConfig", func() {
				invalidObject := generatePkiObject("pki-missing-field")
				invalidObject.Spec.Config.PkiConfig.CertificateAuthority = ""

				err := k8sClient.Create(context.Background(), invalidObject)
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("spec.config.pkiConfig.certificateAuthority: Required value")))
			})

			It("rejects a missing required field in PublicKeyConfig", func() {
				invalidObject := generatePublicKeyObject("publickey-missing-field")
				invalidObject.Spec.Config.PublicKeyConfig.KeyPath = ""

				err := k8sClient.Create(context.Background(), invalidObject)
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("spec.config.publicKeyConfig.keyPath: Required value")))
			})

			It("allows an update to the Model path", func() {
				created := generateSigstoreObject("mutable-model-test")
				Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

				fetched := &ModelValidation{}
				Expect(k8sClient.Get(context.Background(), getKey(created), fetched)).To(Succeed())

				newPath := "/new/path/to/model.onnx"
				fetched.Spec.Model.Path = newPath
				Expect(k8sClient.Update(context.Background(), fetched)).To(Succeed())

				// Fetch and verify the change
				updated := &ModelValidation{}
				Expect(k8sClient.Get(context.Background(), getKey(created), updated)).To(Succeed())
				Expect(updated.Spec.Model.Path).To(Equal(newPath))
			})

			It("allows an update to the Config fields", func() {
				created := generateSigstoreObject("mutable-config-test")
				Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

				fetched := &ModelValidation{}
				Expect(k8sClient.Get(context.Background(), getKey(created), fetched)).To(Succeed())

				// Update the config from Sigstore to PKI
				fetched.Spec.Config.SigstoreConfig = nil
				fetched.Spec.Config.PkiConfig = &PkiConfig{
					CertificateAuthority: "new-ca-path",
				}
				Expect(k8sClient.Update(context.Background(), fetched)).To(Succeed())

				// Fetch and verify the change
				updated := &ModelValidation{}
				Expect(k8sClient.Get(context.Background(), getKey(created), updated)).To(Succeed())
				Expect(updated.Spec.Config.SigstoreConfig).To(BeNil())
				Expect(updated.Spec.Config.PkiConfig).ToNot(BeNil())
				Expect(updated.Spec.Config.PkiConfig.CertificateAuthority).To(Equal("new-ca-path"))
			})

			It("accepts Model with ignore options fields", func() {
				ignoreGitPaths := true
				ignoreUnsignedFiles := false
				allowSymlinks := true

				created := generateSigstoreObject("model-ignore-options")
				created.Spec.Model.IgnorePaths = []string{"/tmp", "/cache"}
				created.Spec.Model.IgnoreGitPaths = &ignoreGitPaths
				created.Spec.Model.IgnoreUnsignedFiles = &ignoreUnsignedFiles
				created.Spec.Model.AllowSymlinks = &allowSymlinks

				Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

				fetched := &ModelValidation{}
				Expect(k8sClient.Get(context.Background(), getKey(created), fetched)).To(Succeed())
				Expect(fetched.Spec.Model.IgnorePaths).To(Equal([]string{"/tmp", "/cache"}))
				Expect(fetched.Spec.Model.IgnoreGitPaths).ToNot(BeNil())
				Expect(*fetched.Spec.Model.IgnoreGitPaths).To(BeTrue())
				Expect(fetched.Spec.Model.IgnoreUnsignedFiles).ToNot(BeNil())
				Expect(*fetched.Spec.Model.IgnoreUnsignedFiles).To(BeFalse())
				Expect(fetched.Spec.Model.AllowSymlinks).ToNot(BeNil())
				Expect(*fetched.Spec.Model.AllowSymlinks).To(BeTrue())
			})

			It("accepts Model without ignore options fields (backward compatibility)", func() {
				created := generateSigstoreObject("model-no-ignore-options")
				Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

				fetched := &ModelValidation{}
				Expect(k8sClient.Get(context.Background(), getKey(created), fetched)).To(Succeed())
				Expect(fetched.Spec.Model.IgnorePaths).To(BeNil())
				Expect(fetched.Spec.Model.IgnoreGitPaths).To(BeNil())
				Expect(fetched.Spec.Model.IgnoreUnsignedFiles).To(BeNil())
				Expect(fetched.Spec.Model.AllowSymlinks).To(BeNil())
			})

			It("allows updating ignore options fields", func() {
				created := generateSigstoreObject("model-update-ignore-options")
				Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

				fetched := &ModelValidation{}
				Expect(k8sClient.Get(context.Background(), getKey(created), fetched)).To(Succeed())

				Expect(fetched.Spec.Model.IgnorePaths).To(BeNil())
				Expect(fetched.Spec.Model.IgnoreGitPaths).To(BeNil())
				Expect(fetched.Spec.Model.IgnoreUnsignedFiles).To(BeNil())
				Expect(fetched.Spec.Model.AllowSymlinks).To(BeNil())

				ignoreGitPaths := false
				ignoreUnsignedFiles := true
				allowSymlinks := false
				fetched.Spec.Model.IgnorePaths = []string{"/opt/test"}
				fetched.Spec.Model.IgnoreGitPaths = &ignoreGitPaths
				fetched.Spec.Model.IgnoreUnsignedFiles = &ignoreUnsignedFiles
				fetched.Spec.Model.AllowSymlinks = &allowSymlinks
				Expect(k8sClient.Update(context.Background(), fetched)).To(Succeed())

				updated := &ModelValidation{}
				Expect(k8sClient.Get(context.Background(), getKey(created), updated)).To(Succeed())
				Expect(updated.Spec.Model.IgnorePaths).To(Equal([]string{"/opt/test"}))
				Expect(updated.Spec.Model.IgnoreGitPaths).ToNot(BeNil())
				Expect(*updated.Spec.Model.IgnoreGitPaths).To(BeFalse())
				Expect(updated.Spec.Model.IgnoreUnsignedFiles).ToNot(BeNil())
				Expect(*updated.Spec.Model.IgnoreUnsignedFiles).To(BeTrue())
				Expect(updated.Spec.Model.AllowSymlinks).ToNot(BeNil())
				Expect(*updated.Spec.Model.AllowSymlinks).To(BeFalse())
			})
		})
	})
})
