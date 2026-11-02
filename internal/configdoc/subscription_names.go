package configdoc

import "proxystack-web/internal/domain"

// OverlaySubscriptionNames 让已应用快照使用当前保存的命名字段，不带入未应用的连接参数和凭据。
func OverlaySubscriptionNames(applied, current *Document) {
	if applied.Settings != nil && current.Settings != nil {
		applied.Settings.Subscription.DisplayTemplate = current.Settings.Subscription.DisplayTemplate
	}
	for userIndex := range applied.Users {
		appliedUser := &applied.Users[userIndex]
		currentIndex := -1
		if appliedUser.ID != 0 {
			currentIndex = current.UserByID(appliedUser.ID)
		}
		if currentIndex < 0 {
			currentIndex = current.UserByName(appliedUser.Name)
		}
		if currentIndex < 0 {
			continue
		}
		currentUser := &current.Users[currentIndex]
		appliedUser.DisplayTemplate = currentUser.DisplayTemplate
		for profileIndex := range appliedUser.Profiles {
			appliedProfile := &appliedUser.Profiles[profileIndex]
			currentProfile := findCurrentProfile(currentUser, appliedProfile)
			if currentProfile == nil {
				continue
			}
			appliedProfile.Remark = currentProfile.Remark
			appliedProfile.DisplayTemplate = currentProfile.DisplayTemplate
		}
	}
	for stackIndex := range applied.Stacks {
		appliedStack := &applied.Stacks[stackIndex]
		currentIndex := -1
		if appliedStack.ID != 0 {
			currentIndex = current.StackByID(appliedStack.ID)
		}
		if currentIndex < 0 {
			currentIndex = current.StackByName(appliedStack.Name)
		}
		if currentIndex < 0 {
			continue
		}
		currentStack := &current.Stacks[currentIndex]
		for inboundIndex := range appliedStack.Xray.Inbounds {
			appliedInbound := &appliedStack.Xray.Inbounds[inboundIndex]
			currentInbound := findCurrentInbound(currentStack, appliedInbound)
			if currentInbound == nil {
				continue
			}
			appliedInbound.Remark = currentInbound.Remark
			appliedInbound.DisplayTemplate = currentInbound.DisplayTemplate
			for refIndex := range appliedInbound.Users {
				appliedRef := &appliedInbound.Users[refIndex]
				currentRef := findCurrentUserRef(currentInbound, appliedRef)
				if currentRef == nil {
					continue
				}
				appliedRef.Remark = currentRef.Remark
				appliedRef.DisplayTemplate = currentRef.DisplayTemplate
			}
		}
	}
}

func findCurrentProfile(user *User, applied *Profile) *Profile {
	for index := range user.Profiles {
		candidate := &user.Profiles[index]
		if applied.ID != 0 && candidate.ID == applied.ID || applied.ID == 0 && candidate.Name == applied.Name {
			return candidate
		}
	}
	return nil
}

func findCurrentInbound(stack *Stack, applied *Inbound) *Inbound {
	for index := range stack.Xray.Inbounds {
		candidate := &stack.Xray.Inbounds[index]
		if applied.ID != 0 && candidate.ID == applied.ID || applied.ID == 0 && candidate.Name == applied.Name {
			return candidate
		}
	}
	return nil
}

func findCurrentUserRef(inbound *Inbound, applied *UserRef) *UserRef {
	for index := range inbound.Users {
		candidate := &inbound.Users[index]
		if candidate.User == applied.User && domain.NormalizeUserProfile(candidate.Profile) == domain.NormalizeUserProfile(applied.Profile) {
			return candidate
		}
	}
	return nil
}
