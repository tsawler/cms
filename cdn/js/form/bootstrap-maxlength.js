var BootstrapMaxlength=function() {
    var e=function() {
    	$("#url").maxlength( {
            threshold: 80, separator: " of ", preText: "You have ", postText: " chars remaining.", warningClass: "m-badge m-badge--info m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide"
        }),
        $("#title").maxlength( {
            threshold: 60, separator: " of ", preText: "You have ", postText: " chars remaining.", warningClass: "m-badge m-badge--info m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide"
        }),
        $("#meta_description").maxlength( {
            threshold: 191, separator: " of ", preText: "You have ", postText: " chars remaining.", warningClass: "m-badge m-badge--info m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide"
        })
        // $("#m_maxlength_1").maxlength( {
        //     alwaysShow: true, separator: " of ", preText: "You have ", postText: " chars remaining.", warningClass: "m-badge m-badge--warning m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide"
        // }),
        // $("#m_maxlength_2").maxlength( {
        //     threshold: 5, warningClass: "m-badge m-badge--danger m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide"
        // }
        // ),
        // $("#m_maxlength_3").maxlength( {
        //     alwaysShow: !0, threshold: 5, warningClass: "m-badge m-badge--primary m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--brand m-badge--rounded m-badge--wide"
        // }
        // ),
        // $("#m_maxlength_4").maxlength( {
        //     threshold: 3, warningClass: "m-badge m-badge--danger m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide", separator: " of ", preText: "You have ", postText: " chars remaining.", validate: !0
        // }
        // ),
        // $("#m_maxlength_5").maxlength( {
        //     threshold: 5, warningClass: "m-badge m-badge--primary m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--brand m-badge--rounded m-badge--wide"
        // }
        // ),
        // $("#m_maxlength_6_1").maxlength( {
        //     alwaysShow: !0, threshold: 5, placement: "top-left", warningClass: "m-badge m-badge--brand m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--brand m-badge--rounded m-badge--wide"
        // }
        // ),
        // $("#m_maxlength_6_2").maxlength( {
        //     alwaysShow: !0, threshold: 5, placement: "top-right", warningClass: "m-badge m-badge--success m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--brand m-badge--rounded m-badge--wide"
        // }
        // ),
        // $("#m_maxlength_6_3").maxlength( {
        //     alwaysShow: !0, threshold: 5, placement: "bottom-left", warningClass: "m-badge m-badge--warning m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--brand m-badge--rounded m-badge--wide"
        // }
        // ),
        // $("#m_maxlength_6_4").maxlength( {
        //     alwaysShow: !0, threshold: 5, placement: "bottom-right", warningClass: "m-badge m-badge--danger m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--brand m-badge--rounded m-badge--wide"
        // }
        // ),
        // $("#m_maxlength_1_modal").maxlength( {
        //     warningClass: "m-badge m-badge--warning m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide", appendToParent: !0
        // }
        // ),
        // $("#m_maxlength_2_modal").maxlength( {
        //     threshold: 5, warningClass: "m-badge m-badge--danger m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide", appendToParent: !0
        // }
        // ),
        // $("#m_maxlength_5_modal").maxlength( {
        //     threshold: 5, warningClass: "m-badge m-badge--primary m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--brand m-badge--rounded m-badge--wide", appendToParent: !0
        // }
        // ),
        // $("#m_maxlength_4_modal").maxlength( {
        //     threshold: 3, warningClass: "m-badge m-badge--danger m-badge--rounded m-badge--wide", limitReachedClass: "m-badge m-badge--success m-badge--rounded m-badge--wide", appendToParent: !0, separator: " of ", preText: "You have ", postText: " chars remaining.", validate: !0
        // }
        // )
    }
    ;
    return {
        init:function() {
            e()
        }
    }
}

();
jQuery(document).ready(function() {
    BootstrapMaxlength.init()
}

);