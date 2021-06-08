package cfg

import (
	"errors"


	"github.com/coreos/go-oidc/v3/oidc"
)

// InitAuthentication 初始化认证
func (c *LocalConfig) InitAuthentication() error {
	if !c.Security.Enable {
		return nil
	}

	if c.Security.Authentication == nil {
		return errors.New("security authentication enable but unset")
	}

	// 初始化jwt token认证
	if c.Security.Authentication.OIDCProvider != nil {
		if c.Security.Authentication.OIDCProvider.Issuer == "" {
			return errors.New("security authentication not found oidc issuer")
		}

		provider, err := oidc.NewProvider(ctx, c.Security.Authentication.OIDCProvider.Issuer)
		if err != nil {
			return errors.New("security authentication init provider fail")
		}


		if c.Security.Authentication.OIDCProvider.Config != nil {
			c.Security.tokenVerifier = provider.Verifier(
				&oidc.Config{
				    ClientID: c.Security.Authentication.OIDCProvider.Config.ClientID,
				    }
				)
		} else {
            c.Security.tokenVerifier = provider.Verifier(&oidc.Config{ClientID: "clientID"})
		}
	}

	return nil
}
