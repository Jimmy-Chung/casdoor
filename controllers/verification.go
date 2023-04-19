// Copyright 2021 The Casdoor Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/casdoor/casdoor/captcha"
	"github.com/casdoor/casdoor/object"
	"github.com/casdoor/casdoor/util"
)

const (
	SignupVerification = "signup"
	ResetVerification  = "reset"
	LoginVerification  = "login"
	ForgetVerification = "forget"
)

func (c *ApiController) getCurrentUser() *object.User {
	var user *object.User
	userId := c.GetSessionUsername()
	if userId == "" {
		user = nil
	} else {
		user = object.GetUser(userId)
	}
	return user
}

// SendVerificationCode ...
// @Title SendVerificationCode
// @Tag Verification API
// @router /send-verification-code [post]
func (c *ApiController) SendVerificationCode() {
	destType := c.Ctx.Request.Form.Get("type")
	dest := c.Ctx.Request.Form.Get("dest")
	countryCode := c.Ctx.Request.Form.Get("countryCode")
	checkType := c.Ctx.Request.Form.Get("checkType")
	clientSecret := c.Ctx.Request.Form.Get("clientSecret")
	captchaToken := c.Ctx.Request.Form.Get("captchaToken")
	applicationId := c.Ctx.Request.Form.Get("applicationId")
	method := c.Ctx.Request.Form.Get("method")
	checkUser := c.Ctx.Request.Form.Get("checkUser")
	remoteAddr := util.GetIPFromRequest(c.Ctx.Request)

	if dest == "" {
		c.ResponseError(c.T("general:Missing parameter") + ": dest.")
		return
	}
	if applicationId == "" {
		c.ResponseError(c.T("general:Missing parameter") + ": applicationId.")
		return
	}
	if checkType == "" {
		c.ResponseError(c.T("general:Missing parameter") + ": checkType.")
		return
	}
	if !strings.Contains(applicationId, "/") {
		c.ResponseError(c.T("verification:Wrong parameter") + ": applicationId.")
		return
	}

	if checkType != "none" {
		if captchaToken == "" {
			c.ResponseError(c.T("general:Missing parameter") + ": captchaToken.")
			return
		}

		if captchaProvider := captcha.GetCaptchaProvider(checkType); captchaProvider == nil {
			c.ResponseError(c.T("general:don't support captchaProvider: ") + checkType)
			return
		} else if isHuman, err := captchaProvider.VerifyCaptcha(captchaToken, clientSecret); err != nil {
			c.ResponseError(err.Error())
			return
		} else if !isHuman {
			c.ResponseError(c.T("verification:Turing test failed."))
			return
		}
	}

	application := object.GetApplication(applicationId)
	organization := object.GetOrganization(util.GetId(application.Owner, application.Organization))
	if organization == nil {
		c.ResponseError(c.T("check:Organization does not exist"))
		return
	}

	var user *object.User
	// checkUser != "", means method is ForgetVerification
	if checkUser != "" {
		owner := application.Organization
		user = object.GetUser(util.GetId(owner, checkUser))
	}

	sendResp := errors.New("invalid dest type")

	switch destType {
	case object.VerifyTypeEmail:
		if !util.IsEmailValid(dest) {
			c.ResponseError(c.T("check:Email is invalid"))
			return
		}

		if method == LoginVerification || method == ForgetVerification {
			if user != nil && util.GetMaskedEmail(user.Email) == dest {
				dest = user.Email
			}

			user = object.GetUserByEmail(organization.Name, dest)
			if user == nil {
				c.ResponseError(c.T("verification:the user does not exist, please sign up first"))
				return
			}
		} else if method == ResetVerification {
			user = c.getCurrentUser()
		}

		provider := application.GetEmailProvider()
		sendResp = object.SendVerificationCodeToEmail(organization, user, provider, remoteAddr, dest)
	case object.VerifyTypePhone:
		if method == LoginVerification || method == ForgetVerification {
			if user != nil && util.GetMaskedPhone(user.Phone) == dest {
				dest = user.Phone
			}

			if user = object.GetUserByPhone(organization.Name, dest); user == nil {
				c.ResponseError(c.T("verification:the user does not exist, please sign up first"))
				return
			}

			countryCode = user.GetCountryCode(countryCode)
		} else if method == ResetVerification {
			if user = c.getCurrentUser(); user != nil {
				countryCode = user.GetCountryCode(countryCode)
			}
		}

		provider := application.GetSmsProvider()
		if phone, ok := util.GetE164Number(dest, countryCode); !ok {
			c.ResponseError(fmt.Sprintf(c.T("verification:Phone number is invalid in your region %s"), countryCode))
			return
		} else {
			sendResp = object.SendVerificationCodeToPhone(organization, user, provider, remoteAddr, phone)
		}
	}

	if sendResp != nil {
		c.ResponseError(sendResp.Error())
	} else {
		c.ResponseOk()
	}
}

// ResetEmailOrPhone ...
// @Tag Account API
// @Title ResetEmailOrPhone
// @router /api/reset-email-or-phone [post]
func (c *ApiController) ResetEmailOrPhone() {
	user, ok := c.RequireSignedInUser()
	if !ok {
		return
	}

	destType := c.Ctx.Request.Form.Get("type")
	dest := c.Ctx.Request.Form.Get("dest")
	code := c.Ctx.Request.Form.Get("code")

	if util.IsStringsEmpty(destType, dest, code) {
		c.ResponseError(c.T("general:Missing parameter"))
		return
	}

	checkDest := dest
	organization := object.GetOrganizationByUser(user)
	if destType == object.VerifyTypePhone {
		if object.HasUserByField(user.Owner, "phone", dest) {
			c.ResponseError(c.T("check:Phone already exists"))
			return
		}

		phoneItem := object.GetAccountItemByName("Phone", organization)
		if phoneItem == nil {
			c.ResponseError(c.T("verification:Unable to get the phone modify rule."))
			return
		}

		if pass, errMsg := object.CheckAccountItemModifyRule(phoneItem, user, c.GetAcceptLanguage()); !pass {
			c.ResponseError(errMsg)
			return
		}
		if checkDest, ok = util.GetE164Number(dest, user.GetCountryCode("")); !ok {
			c.ResponseError(fmt.Sprintf(c.T("verification:Phone number is invalid in your region %s"), user.CountryCode))
			return
		}
	} else if destType == object.VerifyTypeEmail {
		if object.HasUserByField(user.Owner, "email", dest) {
			c.ResponseError(c.T("check:Email already exists"))
			return
		}

		emailItem := object.GetAccountItemByName("Email", organization)
		if emailItem == nil {
			c.ResponseError(c.T("verification:Unable to get the email modify rule."))
			return
		}

		if pass, errMsg := object.CheckAccountItemModifyRule(emailItem, user, c.GetAcceptLanguage()); !pass {
			c.ResponseError(errMsg)
			return
		}
	}
	if result := object.CheckVerificationCode(checkDest, code, c.GetAcceptLanguage()); result.Code != object.VerificationSuccess {
		c.ResponseError(result.Msg)
		return
	}

	switch destType {
	case object.VerifyTypeEmail:
		user.Email = dest
		object.SetUserField(user, "email", user.Email)
	case object.VerifyTypePhone:
		user.Phone = dest
		object.SetUserField(user, "phone", user.Phone)
	default:
		c.ResponseError(c.T("verification:Unknown type"))
		return
	}

	object.DisableVerificationCode(checkDest)
	c.ResponseOk()
}

// VerifyCode
// @Tag Account API
// @Title VerifyCode
// @router /api/verify-code [post]
func (c *ApiController) VerifyCode() {
	var form RequestForm
	err := json.Unmarshal(c.Ctx.Input.RequestBody, &form)
	if err != nil {
		c.ResponseError(err.Error())
		return
	}

	var user *object.User
	if form.Name != "" {
		user = object.GetUserByFields(form.Organization, form.Name)
	}

	var checkDest string
	if strings.Contains(form.Username, "@") {
		if user != nil && util.GetMaskedEmail(user.Email) == form.Username {
			form.Username = user.Email
		}
		checkDest = form.Username
	} else {
		if user != nil && util.GetMaskedPhone(user.Phone) == form.Username {
			form.Username = user.Phone
		}
	}

	if user = object.GetUserByFields(form.Organization, form.Username); user == nil {
		c.ResponseError(fmt.Sprintf(c.T("general:The user: %s doesn't exist"), util.GetId(form.Organization, form.Username)))
		return
	}

	verificationCodeType := object.GetVerifyType(form.Username)
	if verificationCodeType == object.VerifyTypePhone {
		form.CountryCode = user.GetCountryCode(form.CountryCode)
		var ok bool
		if checkDest, ok = util.GetE164Number(form.Username, form.CountryCode); !ok {
			c.ResponseError(fmt.Sprintf(c.T("verification:Phone number is invalid in your region %s"), form.CountryCode))
			return
		}
	}

	if result := object.CheckVerificationCode(checkDest, form.Code, c.GetAcceptLanguage()); result.Code != object.VerificationSuccess {
		c.ResponseError(result.Msg)
		return
	}
	object.DisableVerificationCode(checkDest)
	c.SetSession("verifiedCode", form.Code)

	c.ResponseOk()
}

// VerifyCaptcha ...
// @Title VerifyCaptcha
// @Tag Verification API
// @router /verify-captcha [post]
func (c *ApiController) VerifyCaptcha() {
	captchaType := c.Ctx.Request.Form.Get("captchaType")

	captchaToken := c.Ctx.Request.Form.Get("captchaToken")
	clientSecret := c.Ctx.Request.Form.Get("clientSecret")
	if captchaToken == "" {
		c.ResponseError(c.T("general:Missing parameter") + ": captchaToken.")
		return
	}
	if clientSecret == "" {
		c.ResponseError(c.T("general:Missing parameter") + ": clientSecret.")
		return
	}

	provider := captcha.GetCaptchaProvider(captchaType)
	if provider == nil {
		c.ResponseError(c.T("verification:Invalid captcha provider."))
		return
	}

	isValid, err := provider.VerifyCaptcha(captchaToken, clientSecret)
	if err != nil {
		c.ResponseError(err.Error())
		return
	}

	c.ResponseOk(isValid)
}
